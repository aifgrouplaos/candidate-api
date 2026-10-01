package employee

import (
	"context"
	"encoding/json"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/pkg/apierror"
	"github.com/aifgrouplaos/candidate-api/pkg/utils"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	maxTenantLogins = 200
	defaultLimit    = 20
	maxLimit        = 100
	maxPage         = 1_000_000
	msgStatus       = "Status must be active, inactive, or on_leave."
)

var (
	errForbidden = *errs.Forbidden("You do not have permission to perform this action.")
	phonePattern = regexp.MustCompile(`^\+?[0-9][0-9 ()-]{5,18}[0-9]$`)
)

// Optional records whether a PATCH field was sent, so an explicit null can clear a value.
type Optional[T any] struct {
	Set   bool
	Value T
}

func (o *Optional[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	return json.Unmarshal(data, &o.Value)
}

type CreateEmployeeInput struct {
	FullName     string  `json:"fullName"`
	Email        string  `json:"email"`
	Password     string  `json:"password"`
	Phone        *string `json:"phone"`
	DepartmentID *string `json:"departmentId"`
	Position     *string `json:"position"`
	Status       Status  `json:"status"`
	HireDate     *string `json:"hireDate"`
}

type UpdateEmployeeInput struct {
	Version      *int                      `json:"version"`
	FullName     Optional[string]          `json:"fullName"`
	Email        Optional[string]          `json:"email"`
	Phone        Optional[*string]         `json:"phone"`
	DepartmentID Optional[*string]         `json:"departmentId"`
	Position     Optional[*string]         `json:"position"`
	Status       Optional[Status]          `json:"status"`
	HireDate     Optional[*string]         `json:"hireDate"`
	AvatarURL    Optional[*string]         `json:"avatarUrl"`
	Role         Optional[json.RawMessage] `json:"role"`
}

type ListQuery struct {
	Page         int    `query:"page"`
	Limit        int    `query:"limit"`
	Search       string `query:"search"`
	DepartmentID string `query:"departmentId"`
	Status       string `query:"status"`
	SortBy       string `query:"sortBy"`
	SortOrder    string `query:"sortOrder"`
}

type PageMeta struct {
	Page       int   `json:"page"`
	Limit      int   `json:"limit"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"totalPages"`
}

type EmployeeView struct {
	ID           string      `json:"id"`
	EmployeeCode string      `json:"employeeCode"`
	FullName     string      `json:"fullName"`
	Email        string      `json:"email"`
	Phone        *string     `json:"phone"`
	Department   *Department `json:"department"`
	Position     *string     `json:"position"`
	Status       Status      `json:"status"`
	HireDate     *string     `json:"hireDate"`
	AvatarURL    *string     `json:"avatarUrl"`
	Version      int         `json:"version"`
	CreatedAt    time.Time   `json:"createdAt"`
	UpdatedAt    time.Time   `json:"updatedAt"`
}

type EmployeeUsecase interface {
	List(ctx context.Context, actor auth.Principal, query ListQuery) ([]*EmployeeView, PageMeta, error)
	Get(ctx context.Context, actor auth.Principal, id string) (*EmployeeView, error)
	Create(ctx context.Context, actor auth.Principal, input CreateEmployeeInput) (*EmployeeView, error)
	Update(ctx context.Context, actor auth.Principal, id string, input UpdateEmployeeInput) (*EmployeeView, error)
	Delete(ctx context.Context, actor auth.Principal, id string) error
	Departments(ctx context.Context) ([]Department, error)
}

type employeeUsecase struct {
	repo EmployeeRepository
}

func NewEmployeeUsecase(repo EmployeeRepository) EmployeeUsecase {
	return &employeeUsecase{repo: repo}
}

func (u *employeeUsecase) List(ctx context.Context, actor auth.Principal, query ListQuery) ([]*EmployeeView, PageMeta, error) {
	if actor.Role != auth.RoleAdmin {
		return nil, PageMeta{}, errForbidden
	}
	var v validation
	filter := ListFilter{TenantID: actor.TenantID, Search: strings.TrimSpace(query.Search), SortBy: SortCreatedAt}
	if utf8.RuneCountInString(filter.Search) > 100 {
		v.add("search", "Search must be at most 100 characters.")
	}
	if query.DepartmentID != "" {
		if _, err := uuid.Parse(query.DepartmentID); err != nil {
			v.add("departmentId", "Department ID is invalid.")
		}
		filter.DepartmentID = query.DepartmentID
	}
	for _, raw := range strings.Split(query.Status, ",") {
		status := Status(strings.TrimSpace(raw))
		if status == "" {
			continue
		}
		if !status.Valid() {
			v.add("status", msgStatus)
			break
		}
		filter.Statuses = append(filter.Statuses, status)
	}
	filter.Statuses = utils.Unique(filter.Statuses)
	switch query.SortBy {
	case "", "createdAt":
	case "fullName":
		filter.SortBy = SortFullName
	case "hireDate":
		filter.SortBy = SortHireDate
	default:
		v.add("sortBy", "Sort must be fullName, hireDate, or createdAt.")
	}
	switch query.SortOrder {
	case "", "asc":
	case "desc":
		filter.Desc = true
	default:
		v.add("sortOrder", "Sort order must be asc or desc.")
	}
	if query.Page > maxPage {
		v.add("page", "Page is too large.")
	}
	if err := v.err(); err != nil {
		return nil, PageMeta{}, err
	}
	page, limit := max(query.Page, 1), query.Limit
	if limit < 1 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)
	filter.Offset, filter.Limit = (page-1)*limit, limit

	employees, total, err := u.repo.List(ctx, filter)
	if err != nil {
		return nil, PageMeta{}, err
	}
	views := make([]*EmployeeView, len(employees))
	for i, employee := range employees {
		views[i] = view(employee)
	}
	return views, PageMeta{Page: page, Limit: limit, Total: total, TotalPages: int((total + int64(limit) - 1) / int64(limit))}, nil
}

func (u *employeeUsecase) Get(ctx context.Context, actor auth.Principal, id string) (*EmployeeView, error) {
	employee, err := u.find(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	return view(employee), nil
}

func (u *employeeUsecase) Create(ctx context.Context, actor auth.Principal, input CreateEmployeeInput) (*EmployeeView, error) {
	if actor.Role != auth.RoleAdmin {
		return nil, errForbidden
	}
	var v validation
	employee := &Employee{
		TenantID: actor.TenantID,
		FullName: v.fullName(input.FullName),
		Email:    v.email(input.Email),
		Phone:    v.phone(input.Phone),
		Position: v.position(input.Position),
		Status:   StatusActive,
		HireDate: v.hireDate(input.HireDate),
	}
	if input.Status != "" {
		employee.Status = v.status(input.Status)
	}
	if utf8.RuneCountInString(input.Password) < 8 || len(input.Password) > 72 {
		v.add("password", "Password must be at least 8 characters and at most 72 bytes.")
	}
	departmentID, err := u.department(ctx, &v, input.DepartmentID)
	if err != nil {
		return nil, err
	}
	employee.DepartmentID = departmentID
	if err := v.err(); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, errs.Internal("could not secure the password")
	}
	login := &auth.User{
		TenantID: actor.TenantID, Email: employee.Email, PasswordHash: string(hash),
		Role: auth.RoleEmployee, FullName: employee.FullName, Active: true,
	}
	if err := u.repo.Create(ctx, employee, login, maxTenantLogins); err != nil {
		return nil, err
	}
	return u.Get(ctx, actor, employee.ID)
}

func (u *employeeUsecase) Update(ctx context.Context, actor auth.Principal, id string, input UpdateEmployeeInput) (*EmployeeView, error) {
	employee, err := u.find(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	adminOnly := input.Email.Set || input.DepartmentID.Set || input.Position.Set || input.Status.Set || input.HireDate.Set
	isAdmin := actor.Role == auth.RoleAdmin
	if input.Role.Set || (isAdmin && input.AvatarURL.Set) || (!isAdmin && adminOnly) {
		return nil, errForbidden
	}
	var v validation
	if input.Version == nil {
		v.add("version", "Version is required.")
	}
	if input.FullName.Set {
		employee.FullName = v.fullName(input.FullName.Value)
	}
	if input.Email.Set {
		employee.Email = v.email(input.Email.Value)
	}
	if input.Phone.Set {
		employee.Phone = v.phone(input.Phone.Value)
	}
	if input.Position.Set {
		employee.Position = v.position(input.Position.Value)
	}
	if input.Status.Set {
		employee.Status = v.status(input.Status.Value)
	}
	if input.HireDate.Set {
		employee.HireDate = v.hireDate(input.HireDate.Value)
	}
	if input.AvatarURL.Set {
		employee.AvatarURL = v.avatarURL(input.AvatarURL.Value)
	}
	if input.DepartmentID.Set {
		if employee.DepartmentID, err = u.department(ctx, &v, input.DepartmentID.Value); err != nil {
			return nil, err
		}
	}
	if err := v.err(); err != nil {
		return nil, err
	}
	if err := u.repo.Update(ctx, employee, *input.Version); err != nil {
		return nil, err
	}
	return u.Get(ctx, actor, employee.ID)
}

func (u *employeeUsecase) Delete(ctx context.Context, actor auth.Principal, id string) error {
	if actor.Role != auth.RoleAdmin {
		return errForbidden
	}
	return u.repo.Delete(ctx, actor.TenantID, id, time.Now().UTC())
}

func (u *employeeUsecase) Departments(ctx context.Context) ([]Department, error) {
	return u.repo.Departments(ctx)
}

// find loads an Employee the actor may access. Employees get FORBIDDEN for every other
// ID, existing or not, so they cannot probe their tenant's Employee IDs.
func (u *employeeUsecase) find(ctx context.Context, actor auth.Principal, id string) (*Employee, error) {
	employee, err := u.repo.FindByID(ctx, actor.TenantID, id)
	if actor.Role == auth.RoleAdmin {
		return employee, err
	}
	if appErr, ok := errs.IsAppError(err); ok && appErr.Code == "NOT_FOUND" {
		return nil, errForbidden
	}
	if err != nil {
		return nil, err
	}
	if employee.UserID == nil || *employee.UserID != actor.UserID {
		return nil, errForbidden
	}
	return employee, nil
}

func (u *employeeUsecase) department(ctx context.Context, v *validation, value *string) (*string, error) {
	value = optional(value)
	if value == nil {
		return nil, nil
	}
	if _, err := uuid.Parse(*value); err != nil {
		v.add("departmentId", "Department does not exist.")
		return value, nil
	}
	exists, err := u.repo.DepartmentExists(ctx, *value)
	if err != nil {
		return nil, err
	}
	if !exists {
		v.add("departmentId", "Department does not exist.")
	}
	return value, nil
}

func view(e *Employee) *EmployeeView {
	result := &EmployeeView{
		ID: e.ID, EmployeeCode: e.EmployeeCode, FullName: e.FullName, Email: e.Email, Phone: e.Phone,
		Department: e.Department, Position: e.Position, Status: e.Status, AvatarURL: e.AvatarURL,
		Version: e.Version, CreatedAt: e.CreatedAt.UTC(), UpdatedAt: e.UpdatedAt.UTC(),
	}
	if e.HireDate != nil {
		date := e.HireDate.Format(time.DateOnly)
		result.HireDate = &date
	}
	return result
}

type validation []apierror.FieldError

func (v *validation) add(field, message string) {
	*v = append(*v, apierror.FieldError{Field: field, Message: message})
}

func (v validation) err() error {
	if len(v) == 0 {
		return nil
	}
	return apierror.Validation(v)
}

func (v *validation) fullName(value string) string {
	value = strings.TrimSpace(value)
	if n := utf8.RuneCountInString(value); n < 2 || n > 100 {
		v.add("fullName", "Full name must be 2–100 characters.")
	}
	return value
}

func (v *validation) email(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value || len(value) > 254 {
		v.add("email", "A valid email is required.")
	}
	return value
}

func (v *validation) phone(value *string) *string {
	value = optional(value)
	if value != nil && !phonePattern.MatchString(*value) {
		v.add("phone", "A valid phone number is required.")
	}
	return value
}

func (v *validation) position(value *string) *string {
	value = optional(value)
	if value != nil && utf8.RuneCountInString(*value) > 100 {
		v.add("position", "Position must be at most 100 characters.")
	}
	return value
}

func (v *validation) status(value Status) Status {
	if !value.Valid() {
		v.add("status", msgStatus)
	}
	return value
}

func (v *validation) hireDate(value *string) *time.Time {
	value = optional(value)
	if value == nil {
		return nil
	}
	date, err := time.Parse(time.DateOnly, *value)
	if err != nil {
		v.add("hireDate", "Hire date must be a YYYY-MM-DD date.")
		return nil
	}
	// UTC+14 is the earliest time zone, so this accepts any date that is already today somewhere.
	if date.After(time.Now().UTC().Add(14 * time.Hour)) {
		v.add("hireDate", "Hire date cannot be in the future.")
	}
	return &date
}

func (v *validation) avatarURL(value *string) *string {
	value = optional(value)
	if value == nil {
		return nil
	}
	parsed, err := url.Parse(*value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || len(*value) > 2048 {
		v.add("avatarUrl", "Avatar URL must be an https URL.")
	}
	return value
}

// optional trims a nullable string and treats blank as null.
func optional(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
