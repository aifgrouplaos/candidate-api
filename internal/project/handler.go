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
	lookups := router.Group("/lookups", protected...)
	lookups.Get("/task-types", h.TaskTypes)
	lookups.Get("/priorities", h.Priorities)
	projects := router.Group("/projects", protected...)
	projects.Get("/", h.List)
	projects.Post("/", h.Create)
	projects.Get("/:id", h.Get)
	projects.Delete("/:id", h.Delete)
}

func (h *ProjectHandler) List(c *fiber.Ctx) error {
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

func (h *ProjectHandler) Delete(c *fiber.Ctx) error {
	if err := h.usecase.Delete(c.UserContext(), auth.CurrentPrincipal(c), c.Params("id")); err != nil {
		return err
	}
	return httpresponse.Success(c, nil)
}

func (h *ProjectHandler) TaskTypes(c *fiber.Ctx) error { return httpresponse.Success(c, TaskTypes) }

func (h *ProjectHandler) Priorities(c *fiber.Ctx) error { return httpresponse.Success(c, Priorities) }
