package project

import (
	"strings"

	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/pkg/httpresponse"
	"github.com/gofiber/fiber/v2"
)

type ProjectHandler struct {
	usecase ProjectUsecase
}

func NewProjectHandler(usecase ProjectUsecase) *ProjectHandler {
	return &ProjectHandler{usecase: usecase}
}

func (h *ProjectHandler) RegisterRoutes(router fiber.Router, protected ...fiber.Handler) {
	projects := router.Group("/projects", protected...)
	projects.Post("/", h.Create)
	projects.Get("/:id", h.Get)
}

func (h *ProjectHandler) Create(c *fiber.Ctx) error {
	var input CreateProjectInput
	if err := c.BodyParser(&input); err != nil {
		return errs.ErrBadRequest
	}

	result, err := h.usecase.Create(c.UserContext(), auth.CurrentPrincipal(c), strings.Clone(c.Get("Idempotency-Key")), input)
	if err != nil {
		return err
	}
	return httpresponse.Success(c.Status(fiber.StatusCreated), result)
}

func (h *ProjectHandler) Get(c *fiber.Ctx) error {
	result, err := h.usecase.Get(c.UserContext(), auth.CurrentPrincipal(c), c.Params("id"))
	if err != nil {
		return err
	}
	return httpresponse.Success(c, result)
}
