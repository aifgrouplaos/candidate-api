package employee

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BounkhongDev/bkgo/contract"
	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/pkg/apierror"
	"github.com/aifgrouplaos/candidate-api/pkg/pagination"
	"github.com/aifgrouplaos/candidate-api/pkg/utils"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	maxTenantLogins = 200
	maxAvatarBytes  = 2 << 20
	// avatarURLExpiry is long enough for a page to load the image and short
	// enough that clients must not store it as a permanent identifier.
	avatarURLExpiry = 15 * time.Minute
	msgStatus       = "Status must be active, inactive, or on_leave."
	msgAvatarType   = "Avatar must be a JPG, PNG, or WebP image."
	msgAvatarSize   = "Avatar must be 2 MB or smaller."
	msgNoStorage    = "avatar storage is unavailable"
	msgNoDepartment = "Department does not exist."
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
	pagination.Query
	DepartmentID string `query:"departmentId"`
	Status       string `query:"status"`
	SortBy       string `query:"sortBy"`
	SortOrder    string `query:"sortOrder"`
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

// AvatarFile is one uploaded avatar. Size may be -1 when the caller does not know it.
type AvatarFile struct {
	ContentType string
	Size        int64
	Body        io.Reader
}

type EmployeeUsecase interface {
	List(ctx context.Context, actor auth.Principal, query ListQuery) ([]*EmployeeView, pagination.Meta, error)
	Get(ctx context.Context, actor auth.Principal, id string) (*EmployeeView, error)
	Create(ctx context.Context, actor auth.Principal, input CreateEmployeeInput) (*EmployeeView, error)
	Update(ctx context.Context, actor auth.Principal, id string, input UpdateEmployeeInput) (*EmployeeView, error)
	UploadAvatar(ctx context.Context, actor auth.Principal, id string, file AvatarFile) (*EmployeeView, error)
	Delete(ctx context.Context, actor auth.Principal, id string) error
	Departments(ctx context.Context) ([]Department, error)
}

type employeeUsecase struct {
	repo   EmployeeRepository
	files  contract.Storage
	bucket string
}

func NewEmployeeUsecase(repo EmployeeRepository, files contract.Storage, bucket string) EmployeeUsecase {
	return &employeeUsecase{repo: repo, files: files, bucket: bucket}
}

func (u *employeeUsecase) List(ctx context.Context, actor auth.Principal, query ListQuery) ([]*EmployeeView, pagination.Meta, error) {
	if actor.Role != auth.RoleAdmin {
		return nil, pagination.Meta{}, errForbidden
	}
	var v validation
	filter := ListFilter{TenantID: actor.TenantID, Search: query.Check(&v.FieldErrors), SortBy: SortCreatedAt}
	if query.DepartmentID != "" {
		if _, err := uuid.Parse(query.DepartmentID); err != nil {
			v.Add("departmentId", "Department ID is invalid.")
		}
		filter.DepartmentID = query.DepartmentID
	}
	filter.Statuses = v.statuses(query.Status)
	switch query.SortBy {
	case "", "createdAt":
	case "fullName":
		filter.SortBy = SortFullName
	case "hireDate":
		filter.SortBy = SortHireDate
	default:
		v.Add("sortBy", "Sort must be fullName, hireDate, or createdAt.")
	}
	switch query.SortOrder {
	case "", "asc":
	case "desc":
		filter.Desc = true
	default:
		v.Add("sortOrder", "Sort order must be asc or desc.")
	}
	if err := v.Err(); err != nil {
		return nil, pagination.Meta{}, err
	}
	page, limit, offset := query.Bounds()
	filter.Offset, filter.Limit = offset, limit

	employees, total, err := u.repo.List(ctx, filter)
	if err != nil {
		return nil, pagination.Meta{}, err
	}
	views := make([]*EmployeeView, len(employees))
	for i, employee := range employees {
		views[i], err = u.present(ctx, employee)
		if err != nil {
			return nil, pagination.Meta{}, err
		}
	}
	return views, pagination.NewMeta(page, limit, total), nil
}

func (u *employeeUsecase) Get(ctx context.Context, actor auth.Principal, id string) (*EmployeeView, error) {
	employee, err := u.find(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	return u.present(ctx, employee)
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
		v.Add("password", "Password must be at least 8 characters and at most 72 bytes.")
	}
	departmentID, err := u.department(ctx, &v, input.DepartmentID)
	if err != nil {
		return nil, err
	}
	employee.DepartmentID = departmentID
	if err := v.Err(); err != nil {
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
	// avatarUrl is a presigned response field. Clients change the avatar through UploadAvatar.
	if input.Role.Set || input.AvatarURL.Set || (!isAdmin && adminOnly) {
		return nil, errForbidden
	}
	var v validation
	if input.Version == nil {
		v.Add("version", "Version is required.")
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
	if input.DepartmentID.Set {
		if employee.DepartmentID, err = u.department(ctx, &v, input.DepartmentID.Value); err != nil {
			return nil, err
		}
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	if err := u.repo.Update(ctx, employee, *input.Version); err != nil {
		return nil, err
	}
	return u.Get(ctx, actor, employee.ID)
}

func (u *employeeUsecase) UploadAvatar(ctx context.Context, actor auth.Principal, id string, file AvatarFile) (*EmployeeView, error) {
	employee, err := u.find(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	if u.files == nil {
		return nil, errs.Internal(msgNoStorage)
	}
	data, err := readAvatar(ctx, file)
	if err != nil {
		return nil, err
	}
	contentType, ext, ok := avatarType(file.ContentType, data)
	if !ok {
		return nil, avatarInvalid(msgAvatarType)
	}
	key := fmt.Sprintf("avatars/%s/%s/%s%s", employee.TenantID, employee.ID, uuid.NewString(), ext)
	if _, err := u.files.Upload(ctx, u.bucket, key, bytes.NewReader(data), int64(len(data)), contentType); err != nil {
		slog.Error("avatar upload failed", "error", err, "requestId", requestID(ctx))
		return nil, errs.Internal("could not store the avatar")
	}
	previous := employee.AvatarURL
	employee.AvatarURL = &key
	if err := u.repo.Update(ctx, employee, employee.Version); err != nil {
		if delErr := u.files.Delete(ctx, u.bucket, key); delErr != nil {
			slog.Error("avatar cleanup failed", "error", delErr, "requestId", requestID(ctx))
		}
		return nil, err
	}
	if old, ok := avatarObjectKey(employee.TenantID, previous); ok && old != key {
		// ponytail: a failed delete leaves the previous object in the private bucket.
		// The Employee already points at the new key; the next replacement deletes this one.
		if err := u.files.Delete(ctx, u.bucket, old); err != nil {
			slog.Error("previous avatar delete failed", "error", err, "requestId", requestID(ctx))
		}
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
	value = utils.Optional(value)
	if value == nil {
		return nil, nil
	}
	if _, err := uuid.Parse(*value); err != nil {
		v.Add("departmentId", msgNoDepartment)
		return value, nil
	}
	exists, err := u.repo.DepartmentExists(ctx, *value)
	if err != nil {
		return nil, err
	}
	if !exists {
		v.Add("departmentId", msgNoDepartment)
	}
	return value, nil
}

// present returns a view whose avatarUrl is a presigned download URL.
// The stored object key is not a client-facing identifier.
func (u *employeeUsecase) present(ctx context.Context, e *Employee) (*EmployeeView, error) {
	result := view(e)
	result.AvatarURL = nil
	key, ok := avatarObjectKey(e.TenantID, e.AvatarURL)
	if !ok {
		return result, nil
	}
	if u.files == nil {
		return nil, errs.Internal(msgNoStorage)
	}
	url, err := u.files.URL(ctx, u.bucket, key, avatarURLExpiry)
	if err != nil {
		slog.Error("avatar url failed", "error", err, "requestId", requestID(ctx))
		return nil, errs.Internal("could not authorize the avatar download")
	}
	result.AvatarURL = &url
	return result, nil
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

type validation struct{ apierror.FieldErrors }

func (v *validation) fullName(value string) string {
	value = strings.TrimSpace(value)
	v.Length("fullName", value, 2, 100, "Full name must be 2–100 characters.")
	return value
}

func (v *validation) email(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value || len(value) > 254 {
		v.Add("email", "A valid email is required.")
	}
	return value
}

func (v *validation) phone(value *string) *string {
	value = utils.Optional(value)
	if value != nil && !phonePattern.MatchString(*value) {
		v.Add("phone", "A valid phone number is required.")
	}
	return value
}

func (v *validation) position(value *string) *string {
	value = utils.Optional(value)
	if value != nil {
		v.Length("position", *value, 0, 100, "Position must be at most 100 characters.")
	}
	return value
}

func (v *validation) status(value Status) Status {
	if !value.Valid() {
		v.Add("status", msgStatus)
	}
	return value
}

// statuses parses a comma-separated status filter, reporting only the first invalid value.
func (v *validation) statuses(value string) []Status {
	var result []Status
	for _, raw := range strings.Split(value, ",") {
		status := Status(strings.TrimSpace(raw))
		if status == "" {
			continue
		}
		if !status.Valid() {
			v.Add("status", msgStatus)
			break
		}
		result = append(result, status)
	}
	return utils.Unique(result)
}

func (v *validation) hireDate(value *string) *time.Time {
	value = utils.Optional(value)
	if value == nil {
		return nil
	}
	date, err := time.Parse(time.DateOnly, *value)
	if err != nil {
		v.Add("hireDate", "Hire date must be a YYYY-MM-DD date.")
		return nil
	}
	// UTC+14 is the earliest time zone, so this accepts any date that is already today somewhere.
	if date.After(time.Now().UTC().Add(14 * time.Hour)) {
		v.Add("hireDate", "Hire date cannot be in the future.")
	}
	return &date
}

func readAvatar(ctx context.Context, file AvatarFile) ([]byte, error) {
	if file.Size > maxAvatarBytes {
		return nil, avatarInvalid(msgAvatarSize)
	}
	if file.Body == nil {
		return nil, avatarInvalid(msgAvatarType)
	}
	data, err := io.ReadAll(io.LimitReader(file.Body, maxAvatarBytes+1))
	if err != nil {
		slog.Error("avatar read failed", "error", err, "requestId", requestID(ctx))
		return nil, errs.Internal("could not read the avatar")
	}
	if len(data) > maxAvatarBytes {
		return nil, avatarInvalid(msgAvatarSize)
	}
	return data, nil
}

func avatarInvalid(message string) error {
	return apierror.Validation([]apierror.FieldError{{Field: "file", Message: message}})
}

// avatarObjectKey accepts only keys this API generated for the Employee's tenant.
func avatarObjectKey(tenantID string, value *string) (string, bool) {
	if value == nil || tenantID == "" {
		return "", false
	}
	key := *value
	if !strings.HasPrefix(key, "avatars/"+tenantID+"/") || strings.Contains(key, "..") || strings.Contains(key, `\`) {
		return "", false
	}
	return key, true
}

func avatarType(declared string, data []byte) (contentType, ext string, ok bool) {
	declared = strings.ToLower(strings.TrimSpace(declared))
	if i := strings.Index(declared, ";"); i >= 0 {
		declared = strings.TrimSpace(declared[:i])
	}
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		contentType, ext = "image/jpeg", ".jpg"
	case bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}):
		contentType, ext = "image/png", ".png"
	case len(data) >= 12 && bytes.Equal(data[0:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		contentType, ext = "image/webp", ".webp"
	default:
		return "", "", false
	}
	if declared == "image/jpg" {
		declared = "image/jpeg"
	}
	if declared != contentType {
		return "", "", false
	}
	return contentType, ext, true
}
