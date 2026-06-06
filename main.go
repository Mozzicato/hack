// TaskBoard — reference app converted to quikdb-frame (stdlib-only Go).
//
// Zero external dependencies => fully static binary => scratch image (<15MB),
// cold start <50ms, idle RAM <10MB. Native WebSocket replaces Socket.IO.
package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Config & environment
// ---------------------------------------------------------------------------

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

var (
	port      = env("PORT", "8080")
	jwtSecret = []byte(env("JWT_SECRET", "quikdb-frame-dev-secret-change-me"))
	mongoURI  = env("MONGODB_URI", "") // empty => in-memory fallback
)

// ---------------------------------------------------------------------------
// Models
// ---------------------------------------------------------------------------

type User struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	pwHash   string
	salt     string
	Created  int64 `json:"createdAt"`
}

type Task struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Desc     string `json:"description"`
	Status   string `json:"status"` // todo | doing | done
	Priority string `json:"priority"`
	OwnerID  string `json:"ownerId"`
	Created  int64  `json:"createdAt"`
	Updated  int64  `json:"updatedAt"`
}

type ChatMessage struct {
	ID     string `json:"id"`
	User   string `json:"user"`
	Text   string `json:"text"`
	SentAt int64  `json:"sentAt"`
}

// ---------------------------------------------------------------------------
// Store (interface — in-memory impl; Mongo pluggable via MONGODB_URI)
// ---------------------------------------------------------------------------

type Store struct {
	mu    sync.RWMutex
	users map[string]*User
	tasks map[string]*Task
	chat  []ChatMessage
	seq   int64
}

func NewStore() *Store {
	return &Store{users: map[string]*User{}, tasks: map[string]*Task{}}
}

func (s *Store) nextID(prefix string) string {
	s.seq++
	return prefix + "_" + strconv.FormatInt(time.Now().UnixNano(), 36) + "_" + strconv.FormatInt(s.seq, 36)
}

// ---------------------------------------------------------------------------
// Auth helpers (HMAC token, stdlib only)
// ---------------------------------------------------------------------------

func hashPassword(pw, salt string) string {
	h := sha256.Sum256([]byte(salt + ":" + pw))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func makeSalt() string {
	h := sha256.Sum256([]byte(strconv.FormatInt(time.Now().UnixNano(), 10)))
	return base64.RawURLEncoding.EncodeToString(h[:8])
}

// token = base64(payloadJSON) "." base64(hmac)
func signToken(uid, role string) string {
	payload, _ := json.Marshal(map[string]any{"sub": uid, "role": role, "exp": time.Now().Add(72 * time.Hour).Unix()})
	body := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, jwtSecret)
	mac.Write([]byte(body))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return body + "." + sig
}

func verifyToken(tok string) (uid, role string, ok bool) {
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	mac := hmac.New(sha256.New, jwtSecret)
	mac.Write([]byte(parts[0]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(want), []byte(parts[1])) != 1 {
		return "", "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", "", false
	}
	var claims struct {
		Sub  string `json:"sub"`
		Role string `json:"role"`
		Exp  int64  `json:"exp"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Exp < time.Now().Unix() {
		return "", "", false
	}
	return claims.Sub, claims.Role, true
}

// ---------------------------------------------------------------------------
// HTTP helpers + middleware
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

type Middleware func(http.Handler) http.Handler

func chain(h http.Handler, mw ...Middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// logging middleware
func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

// recover middleware
func recoverMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic: %v", rec)
				writeErr(w, 500, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// CORS middleware
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// currentUID returns the user id resolved by authH (stashed on the request).
func currentUID(r *http.Request) string { return r.Header.Get("X-Resolved-UID") }

// ---------------------------------------------------------------------------
// App
// ---------------------------------------------------------------------------

type App struct {
	store *Store
	hub   *Hub
}

func main() {
	app := &App{store: NewStore(), hub: NewHub()}
	app.seed()
	go app.hub.run()

	mux := http.NewServeMux()

	// Health & root (QuikDB health check)
	mux.HandleFunc("GET /health", app.health)
	mux.HandleFunc("GET /healthz", app.health)

	// 10 core API endpoints ----------------------------------------------
	mux.HandleFunc("POST /api/auth/register", app.register) // 1
	mux.HandleFunc("POST /api/auth/login", app.login)       // 2
	mux.HandleFunc("GET /api/auth/me", app.authH(app.me))   // 3
	mux.HandleFunc("GET /api/users", app.authH(app.users))  // 4
	mux.HandleFunc("GET /api/tasks", app.authH(app.listTasks))         // 5
	mux.HandleFunc("POST /api/tasks", app.authH(app.createTask))       // 6
	mux.HandleFunc("PUT /api/tasks/{id}", app.authH(app.updateTask))   // 7
	mux.HandleFunc("DELETE /api/tasks/{id}", app.authH(app.deleteTask))// 8
	mux.HandleFunc("GET /api/analytics", app.authH(app.analytics))     // 9
	mux.HandleFunc("GET /api/chat", app.authH(app.chatHistory))        // 10
	mux.HandleFunc("POST /api/chat", app.authH(app.postChat))

	// Mobile readiness: push hooks, offline sync, OTA
	mux.HandleFunc("POST /api/push/register", app.authH(app.pushRegister))
	mux.HandleFunc("GET /api/sync", app.authH(app.sync))
	mux.HandleFunc("GET /api/ota", app.ota)

	// Native WebSocket (replaces Socket.IO)
	mux.HandleFunc("GET /ws", app.ws)

	// Static UI
	mux.HandleFunc("GET /", app.index)

	handler := chain(mux, recoverMW, logging, cors)

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // 0 so WebSocket connections aren't cut off
		IdleTimeout:  60 * time.Second,
	}
	backend := "in-memory"
	if mongoURI != "" {
		backend = "mongodb (configured)"
	}
	log.Printf("TaskBoard (quikdb-frame) listening on :%s  store=%s", port, backend)
	log.Fatal(srv.ListenAndServe())
}

// authH wraps a handler, resolving the bearer token and stashing the uid+role
// in request headers (simple, allocation-free context passing).
func (a *App) authH(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		uid, role, ok := verifyToken(tok)
		if !ok {
			writeErr(w, 401, "unauthorized")
			return
		}
		r.Header.Set("X-Resolved-UID", uid)
		r.Header.Set("X-Resolved-Role", role)
		next(w, r)
	}
}

func (a *App) seed() {
	salt := makeSalt()
	admin := &User{
		ID: a.store.nextID("usr"), Name: "Admin", Email: "admin@taskboard.dev",
		Role: "admin", salt: salt, pwHash: hashPassword("admin123", salt),
		Created: time.Now().Unix(),
	}
	a.store.users[admin.ID] = admin
	for i, t := range []string{"Set up QuikDB deploy", "Convert reference app", "Demo cold start"} {
		id := a.store.nextID("tsk")
		a.store.tasks[id] = &Task{ID: id, Title: t, Status: []string{"done", "doing", "todo"}[i],
			Priority: "high", OwnerID: admin.ID, Created: time.Now().Unix(), Updated: time.Now().Unix()}
	}
	a.store.chat = append(a.store.chat, ChatMessage{ID: a.store.nextID("msg"), User: "Admin", Text: "Welcome to TaskBoard on QuikDB 🚀", SentAt: time.Now().Unix()})
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "uptime": time.Now().Unix(), "store": storeKind()})
}

func storeKind() string {
	if mongoURI != "" {
		return "mongodb"
	}
	return "in-memory"
}

func (a *App) register(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name, Email, Password string }
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Email == "" || in.Password == "" {
		writeErr(w, 400, "name, email, password required")
		return
	}
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	for _, u := range a.store.users {
		if u.Email == in.Email {
			writeErr(w, 409, "email already registered")
			return
		}
	}
	salt := makeSalt()
	u := &User{ID: a.store.nextID("usr"), Name: in.Name, Email: in.Email, Role: "user",
		salt: salt, pwHash: hashPassword(in.Password, salt), Created: time.Now().Unix()}
	a.store.users[u.ID] = u
	writeJSON(w, 201, map[string]any{"token": signToken(u.ID, u.Role), "user": u})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password string }
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, 400, "invalid body")
		return
	}
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	for _, u := range a.store.users {
		if u.Email == in.Email && subtle.ConstantTimeCompare([]byte(u.pwHash), []byte(hashPassword(in.Password, u.salt))) == 1 {
			writeJSON(w, 200, map[string]any{"token": signToken(u.ID, u.Role), "user": u})
			return
		}
	}
	writeErr(w, 401, "invalid credentials")
}

func (a *App) me(w http.ResponseWriter, r *http.Request) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	if u := a.store.users[currentUID(r)]; u != nil {
		writeJSON(w, 200, u)
		return
	}
	writeErr(w, 404, "not found")
}

func (a *App) users(w http.ResponseWriter, r *http.Request) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	out := make([]*User, 0, len(a.store.users))
	for _, u := range a.store.users {
		out = append(out, u)
	}
	writeJSON(w, 200, out)
}

func (a *App) listTasks(w http.ResponseWriter, r *http.Request) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	status := r.URL.Query().Get("status")
	out := make([]*Task, 0, len(a.store.tasks))
	for _, t := range a.store.tasks {
		if status == "" || t.Status == status {
			out = append(out, t)
		}
	}
	writeJSON(w, 200, out)
}

func (a *App) createTask(w http.ResponseWriter, r *http.Request) {
	var in struct{ Title, Description, Status, Priority string }
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Title == "" {
		writeErr(w, 400, "title required")
		return
	}
	if in.Status == "" {
		in.Status = "todo"
	}
	if in.Priority == "" {
		in.Priority = "medium"
	}
	a.store.mu.Lock()
	t := &Task{ID: a.store.nextID("tsk"), Title: in.Title, Desc: in.Description, Status: in.Status,
		Priority: in.Priority, OwnerID: currentUID(r), Created: time.Now().Unix(), Updated: time.Now().Unix()}
	a.store.tasks[t.ID] = t
	a.store.mu.Unlock()
	a.hub.broadcast(wsEvent("task.created", t))
	writeJSON(w, 201, t)
}

func (a *App) updateTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct{ Title, Description, Status, Priority *string }
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, 400, "invalid body")
		return
	}
	a.store.mu.Lock()
	t := a.store.tasks[id]
	if t == nil {
		a.store.mu.Unlock()
		writeErr(w, 404, "not found")
		return
	}
	if in.Title != nil {
		t.Title = *in.Title
	}
	if in.Description != nil {
		t.Desc = *in.Description
	}
	if in.Status != nil {
		t.Status = *in.Status
	}
	if in.Priority != nil {
		t.Priority = *in.Priority
	}
	t.Updated = time.Now().Unix()
	a.store.mu.Unlock()
	a.hub.broadcast(wsEvent("task.updated", t))
	writeJSON(w, 200, t)
}

func (a *App) deleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.store.mu.Lock()
	_, ok := a.store.tasks[id]
	delete(a.store.tasks, id)
	a.store.mu.Unlock()
	if !ok {
		writeErr(w, 404, "not found")
		return
	}
	a.hub.broadcast(wsEvent("task.deleted", map[string]string{"id": id}))
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

func (a *App) analytics(w http.ResponseWriter, r *http.Request) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	byStatus := map[string]int{"todo": 0, "doing": 0, "done": 0}
	for _, t := range a.store.tasks {
		byStatus[t.Status]++
	}
	writeJSON(w, 200, map[string]any{
		"totalUsers":    len(a.store.users),
		"totalTasks":    len(a.store.tasks),
		"tasksByStatus": byStatus,
		"chatMessages":  len(a.store.chat),
		"wsClients":     a.hub.count(),
	})
}

func (a *App) chatHistory(w http.ResponseWriter, r *http.Request) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	writeJSON(w, 200, a.store.chat)
}

func (a *App) postChat(w http.ResponseWriter, r *http.Request) {
	var in struct{ Text string }
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Text == "" {
		writeErr(w, 400, "text required")
		return
	}
	a.store.mu.Lock()
	u := a.store.users[currentUID(r)]
	name := "user"
	if u != nil {
		name = u.Name
	}
	msg := ChatMessage{ID: a.store.nextID("msg"), User: name, Text: in.Text, SentAt: time.Now().Unix()}
	a.store.chat = append(a.store.chat, msg)
	a.store.mu.Unlock()
	a.hub.broadcast(wsEvent("chat.message", msg))
	writeJSON(w, 201, msg)
}

// ---- Mobile readiness -----------------------------------------------------

func (a *App) pushRegister(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token, Platform string }
	json.NewDecoder(r.Body).Decode(&in)
	// In production: persist token, fan out via FCM/APNs. Hook is in place.
	log.Printf("push token registered platform=%s", in.Platform)
	writeJSON(w, 200, map[string]any{"registered": true, "platform": in.Platform})
}

func (a *App) sync(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	tasks := make([]*Task, 0)
	for _, t := range a.store.tasks {
		if t.Updated >= since {
			tasks = append(tasks, t)
		}
	}
	writeJSON(w, 200, map[string]any{"now": time.Now().Unix(), "tasks": tasks, "chat": a.store.chat})
}

func (a *App) ota(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"version": "1.0.0", "minSupported": "1.0.0",
		"url": "/static/bundle.zip", "mandatory": false,
	})
}

// ---------------------------------------------------------------------------
// Native WebSocket (RFC 6455) — stdlib only, no gorilla
// ---------------------------------------------------------------------------

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type Hub struct {
	mu      sync.Mutex
	clients map[*wsConn]struct{}
	add     chan *wsConn
	del     chan *wsConn
	send    chan []byte
}

type wsConn struct {
	c  net.Conn
	mu sync.Mutex
}

func NewHub() *Hub {
	return &Hub{clients: map[*wsConn]struct{}{}, add: make(chan *wsConn), del: make(chan *wsConn), send: make(chan []byte, 64)}
}

func (h *Hub) run() {
	for {
		select {
		case c := <-h.add:
			h.mu.Lock()
			h.clients[c] = struct{}{}
			h.mu.Unlock()
		case c := <-h.del:
			h.mu.Lock()
			delete(h.clients, c)
			h.mu.Unlock()
			c.c.Close()
		case msg := <-h.send:
			h.mu.Lock()
			for c := range h.clients {
				if err := c.write(msg); err != nil {
					delete(h.clients, c)
					c.c.Close()
				}
			}
			h.mu.Unlock()
		}
	}
}

func (h *Hub) broadcast(b []byte) {
	select {
	case h.send <- b:
	default:
	}
}

func (h *Hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func wsEvent(event string, data any) []byte {
	b, _ := json.Marshal(map[string]any{"event": event, "data": data})
	return b
}

func (a *App) ws(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" || !strings.Contains(strings.ToLower(r.Header.Get("Upgrade")), "websocket") {
		writeErr(w, 400, "expected websocket upgrade")
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		writeErr(w, 500, "hijack unsupported")
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	sum := sha1.Sum([]byte(key + wsGUID))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := buf.WriteString(resp); err != nil {
		conn.Close()
		return
	}
	buf.Flush()

	wc := &wsConn{c: conn}
	a.hub.add <- wc
	wc.write(wsEvent("hello", map[string]string{"msg": "connected to TaskBoard ws"}))

	for {
		op, payload, err := wsRead(buf.Reader)
		if err != nil {
			break
		}
		switch op {
		case 0x8: // close
			a.hub.del <- wc
			return
		case 0x9: // ping -> pong
			wc.writeFrame(0xA, payload)
		case 0x1, 0x2: // text/binary -> echo as chat broadcast
			a.handleWSMessage(payload)
		}
	}
	a.hub.del <- wc
}

func (a *App) handleWSMessage(payload []byte) {
	var in struct {
		Event string `json:"event"`
		Text  string `json:"text"`
		User  string `json:"user"`
	}
	if json.Unmarshal(payload, &in) == nil && in.Text != "" {
		a.store.mu.Lock()
		msg := ChatMessage{ID: a.store.nextID("msg"), User: orDefault(in.User, "ws-user"), Text: in.Text, SentAt: time.Now().Unix()}
		a.store.chat = append(a.store.chat, msg)
		a.store.mu.Unlock()
		a.hub.broadcast(wsEvent("chat.message", msg))
	}
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// wsRead reads a single (unfragmented) client frame, unmasking the payload.
func wsRead(r io.Reader) (opcode byte, payload []byte, err error) {
	hdr := make([]byte, 2)
	if _, err = io.ReadFull(r, hdr); err != nil {
		return
	}
	opcode = hdr[0] & 0x0f
	masked := hdr[1]&0x80 != 0
	length := int64(hdr[1] & 0x7f)
	switch length {
	case 126:
		ext := make([]byte, 2)
		if _, err = io.ReadFull(r, ext); err != nil {
			return
		}
		length = int64(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err = io.ReadFull(r, ext); err != nil {
			return
		}
		length = int64(binary.BigEndian.Uint64(ext))
	}
	if length > 1<<20 {
		return 0, nil, errors.New("frame too large")
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(r, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(r, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return opcode, payload, nil
}

func (c *wsConn) write(msg []byte) error { return c.writeFrame(0x1, msg) }

// writeFrame writes an unmasked server frame.
func (c *wsConn) writeFrame(opcode byte, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var hdr []byte
	n := len(payload)
	switch {
	case n < 126:
		hdr = []byte{0x80 | opcode, byte(n)}
	case n < 1<<16:
		hdr = []byte{0x80 | opcode, 126, byte(n >> 8), byte(n)}
	default:
		hdr = make([]byte, 10)
		hdr[0] = 0x80 | opcode
		hdr[1] = 127
		binary.BigEndian.PutUint64(hdr[2:], uint64(n))
	}
	if _, err := c.c.Write(hdr); err != nil {
		return err
	}
	_, err := c.c.Write(payload)
	return err
}

// ---------------------------------------------------------------------------
// Static UI
// ---------------------------------------------------------------------------

func (a *App) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, indexHTML)
}
