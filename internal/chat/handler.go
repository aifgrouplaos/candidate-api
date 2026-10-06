package chat

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/pkg/httpresponse"
	"github.com/aifgrouplaos/candidate-api/pkg/realtime"
	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
)

const (
	pingInterval = 25 * time.Second
	readTimeout  = 2 * pingInterval
	writeTimeout = 10 * time.Second
	// maxEventBytes and maxEventsPerMinute bound what one connection may send.
	maxEventBytes      = 4096
	maxEventsPerMinute = 120
	closeUnauthorized  = 4401
)

type ChatHandler struct {
	usecase ChatUsecase
}

func NewChatHandler(usecase ChatUsecase) *ChatHandler {
	return &ChatHandler{usecase: usecase}
}

func (h *ChatHandler) RegisterRoutes(router fiber.Router, protected ...fiber.Handler) {
	chat := router.Group("/chat", protected...)
	chat.Get("/unread-count", h.UnreadCount)
	chat.Post("/ws-ticket", h.IssueTicket)
	conversations := chat.Group("/conversations")
	conversations.Get("/", h.List)
	conversations.Post("/", h.Open)
	conversations.Get("/:id", h.Get)
	conversations.Get("/:id/messages", h.Messages)
	conversations.Post("/:id/messages", h.Send)
	conversations.Post("/:id/read", h.MarkRead)
}

// RegisterWebSocket serves /ws/chat. Browsers always send Origin, so a missing Origin is
// a non-browser client, which cannot be a cross-site attack; the ticket still applies.
func (h *ChatHandler) RegisterWebSocket(router fiber.Router, allowedOrigins []string) {
	router.Get("/ws/chat", func(c *fiber.Ctx) error {
		if !websocket.IsWebSocketUpgrade(c) {
			return fiber.ErrUpgradeRequired
		}
		return c.Next()
	}, websocket.New(func(conn *websocket.Conn) {
		origin := conn.Headers(fiber.HeaderOrigin)
		if origin != "" && !slices.Contains(allowedOrigins, origin) {
			closeWith(conn, closeUnauthorized, "Origin is not allowed.")
			return
		}
		h.serve(conn)
	}))
}

func (h *ChatHandler) IssueTicket(c *fiber.Ctx) error {
	result, err := h.usecase.IssueTicket(c.UserContext(), auth.CurrentPrincipal(c))
	if err != nil {
		return err
	}
	return httpresponse.Success(c, result)
}

// serve reads client events while a second goroutine, the only writer, sends queued
// events and pings and closes the connection when the session ends.
func (h *ChatHandler) serve(conn *websocket.Conn) {
	ctx := context.Background()
	session, err := h.usecase.Connect(ctx, conn.Query("ticket"))
	switch {
	case errors.Is(err, errs.ErrUnauthorized):
		closeWith(conn, closeUnauthorized, "Authentication failed.")
		return
	case errors.Is(err, realtime.ErrTooManySessions):
		closeWith(conn, websocket.ClosePolicyViolation, "Too many chat sessions.")
		return
	case err != nil:
		closeWith(conn, websocket.CloseInternalServerErr, "An unexpected error occurred.")
		return
	}
	written := make(chan struct{})
	go func() {
		defer close(written)
		h.write(ctx, conn, session)
	}()
	// The connection is released when serve returns, so the writer must stop first.
	defer func() {
		h.usecase.Disconnect(ctx, session)
		<-written
	}()

	conn.SetReadLimit(maxEventBytes)
	extend := func(string) error { return conn.SetReadDeadline(time.Now().Add(readTimeout)) }
	_ = extend("")
	conn.SetPongHandler(extend)
	// ponytail: fixed one-minute window, so a burst across a boundary can reach twice the
	// limit; a sliding window would smooth it if clients abuse that.
	windowStart, events := time.Now(), 0
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = extend("")
		if time.Since(windowStart) > time.Minute {
			windowStart, events = time.Now(), 0
		}
		if events++; events > maxEventsPerMinute {
			closeWith(conn, websocket.ClosePolicyViolation, "Rate limit exceeded.")
			return
		}
		h.usecase.Receive(ctx, session, raw)
	}
}

func (h *ChatHandler) write(ctx context.Context, conn *websocket.Conn, session *ChatSession) {
	defer conn.Close()
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case event, ok := <-session.Outbox():
			if !ok {
				closeWith(conn, websocket.CloseTryAgainLater, "Session closed; reconnect and resync.")
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if conn.WriteMessage(websocket.TextMessage, event) != nil {
				return
			}
		case <-ping.C:
			if !h.usecase.Active(ctx, session) {
				closeWith(conn, closeUnauthorized, "Authentication session ended.")
				return
			}
			if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeTimeout)) != nil {
				return
			}
		}
	}
}

func closeWith(conn *websocket.Conn, code int, reason string) {
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(writeTimeout))
}

func (h *ChatHandler) Send(c *fiber.Ctx) error {
	var input SendInput
	if err := c.BodyParser(&input); err != nil {
		return errs.ErrBadRequest
	}
	result, err := h.usecase.Send(c.UserContext(), auth.CurrentPrincipal(c), c.Params("id"), input)
	if err != nil {
		return err
	}
	return httpresponse.Success(c.Status(fiber.StatusCreated), result)
}

func (h *ChatHandler) MarkRead(c *fiber.Ctx) error {
	var input ReadInput
	if err := c.BodyParser(&input); err != nil {
		return errs.ErrBadRequest
	}
	if err := h.usecase.MarkRead(c.UserContext(), auth.CurrentPrincipal(c), c.Params("id"), input); err != nil {
		return err
	}
	return httpresponse.Success(c, nil)
}

func (h *ChatHandler) UnreadCount(c *fiber.Ctx) error {
	result, err := h.usecase.UnreadCount(c.UserContext(), auth.CurrentPrincipal(c))
	if err != nil {
		return err
	}
	return httpresponse.Success(c, result)
}

func (h *ChatHandler) List(c *fiber.Ctx) error {
	var query ListQuery
	if err := c.QueryParser(&query); err != nil {
		return errs.ErrBadRequest
	}
	result, meta, err := h.usecase.List(c.UserContext(), auth.CurrentPrincipal(c), query)
	if err != nil {
		return err
	}
	return httpresponse.Page(c, result, meta)
}

func (h *ChatHandler) Open(c *fiber.Ctx) error {
	result, err := h.usecase.Open(c.UserContext(), auth.CurrentPrincipal(c))
	if err != nil {
		return err
	}
	return httpresponse.Success(c, result)
}

func (h *ChatHandler) Get(c *fiber.Ctx) error {
	result, err := h.usecase.Get(c.UserContext(), auth.CurrentPrincipal(c), c.Params("id"))
	if err != nil {
		return err
	}
	return httpresponse.Success(c, result)
}

func (h *ChatHandler) Messages(c *fiber.Ctx) error {
	var query MessageQuery
	if err := c.QueryParser(&query); err != nil {
		return errs.ErrBadRequest
	}
	result, meta, err := h.usecase.Messages(c.UserContext(), auth.CurrentPrincipal(c), c.Params("id"), query)
	if err != nil {
		return err
	}
	return httpresponse.Page(c, result, meta)
}
