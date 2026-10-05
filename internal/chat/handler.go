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
	conversations := router.Group("/chat/conversations", protected...)
	conversations.Get("/", h.List)
	conversations.Post("/", h.Open)
	conversations.Get("/:id", h.Get)
	conversations.Get("/:id/messages", h.Messages)
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
