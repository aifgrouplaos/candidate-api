package employee

import (
	"context"
	"strings"

	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/pkg/httpresponse"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type EmployeeHandler struct {
	usecase EmployeeUsecase
}

func NewEmployeeHandler(usecase EmployeeUsecase) *EmployeeHandler {
	return &EmployeeHandler{usecase: usecase}
}

func (h *EmployeeHandler) RegisterRoutes(router fiber.Router, protected ...fiber.Handler) {
	router.Get("/departments", append(protected, h.Departments)...)
	employees := router.Group("/employees", protected...)
	employees.Get("/", h.List)
	employees.Post("/", h.Create)
	employees.Get("/:id", h.Get)
	employees.Patch("/:id", h.Update)
	employees.Post("/:id/avatar", h.UploadAvatar)
	employees.Delete("/:id", h.Delete)
}

func (h *EmployeeHandler) List(c *fiber.Ctx) error {
	var query ListQuery
	if err := c.QueryParser(&query); err != nil {
		return errs.ErrBadRequest
	}
	result, meta, err := h.usecase.List(requestContext(c), auth.CurrentPrincipal(c), query)
	if err != nil {
		return err
	}
	return httpresponse.Page(c, result, meta)
}

func (h *EmployeeHandler) Get(c *fiber.Ctx) error {
	result, err := h.usecase.Get(requestContext(c), auth.CurrentPrincipal(c), c.Params("id"))
	if err != nil {
		return err
	}
	return httpresponse.Success(c, result)
}

func (h *EmployeeHandler) Create(c *fiber.Ctx) error {
	var input CreateEmployeeInput
	if err := c.BodyParser(&input); err != nil {
		return errs.ErrBadRequest
	}
	result, err := h.usecase.Create(requestContext(c), auth.CurrentPrincipal(c), input)
	if err != nil {
		return err
	}
	return httpresponse.Success(c.Status(fiber.StatusCreated), result)
}

func (h *EmployeeHandler) Update(c *fiber.Ctx) error {
	var input UpdateEmployeeInput
	if err := c.BodyParser(&input); err != nil {
		return errs.ErrBadRequest
	}
	result, err := h.usecase.Update(requestContext(c), auth.CurrentPrincipal(c), c.Params("id"), input)
	if err != nil {
		return err
	}
	return httpresponse.Success(c, result)
}

func (h *EmployeeHandler) UploadAvatar(c *fiber.Ctx) error {
	header, err := c.FormFile("file")
	if err != nil {
		return errs.ErrBadRequest
	}
	file, err := header.Open()
	if err != nil {
		return errs.ErrBadRequest
	}
	defer file.Close()
	result, err := h.usecase.UploadAvatar(requestContext(c), auth.CurrentPrincipal(c), c.Params("id"), AvatarFile{
		ContentType: header.Header.Get("Content-Type"),
		Size:        header.Size,
		Body:        file,
	})
	if err != nil {
		return err
	}
	return httpresponse.Success(c, result)
}

func (h *EmployeeHandler) Delete(c *fiber.Ctx) error {
	if err := h.usecase.Delete(requestContext(c), auth.CurrentPrincipal(c), c.Params("id")); err != nil {
		return err
	}
	return httpresponse.Success(c, nil)
}

func (h *EmployeeHandler) Departments(c *fiber.Ctx) error {
	result, err := h.usecase.Departments(requestContext(c))
	if err != nil {
		return err
	}
	return httpresponse.Success(c, result)
}

type requestIDKey struct{}

// requestContext keeps the response request ID and the ID written to security logs the same.
func requestContext(c *fiber.Ctx) context.Context {
	id := strings.TrimSpace(c.Get(fiber.HeaderXRequestID))
	if id == "" {
		id = uuid.NewString()
		c.Request().Header.Set(fiber.HeaderXRequestID, id)
	}
	return context.WithValue(c.UserContext(), requestIDKey{}, id)
}

func requestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}
