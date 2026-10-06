package chat

import (
	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/pkg/httpresponse"
	"github.com/gofiber/fiber/v2"
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
	conversations := chat.Group("/conversations")
	conversations.Get("/", h.List)
	conversations.Post("/", h.Open)
	conversations.Get("/:id", h.Get)
	conversations.Get("/:id/messages", h.Messages)
	conversations.Post("/:id/messages", h.Send)
	conversations.Post("/:id/read", h.MarkRead)
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
