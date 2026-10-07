package employee

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/BounkhongDev/bkgo/contract"
	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/pkg/apierror"
	"github.com/aifgrouplaos/candidate-api/pkg/pagination"
	"golang.org/x/crypto/bcrypt"
)

const (
	departmentIT = "11111111-1111-1111-1111-111111111111"
	tenantA      = "aaaaaaaa-0000-0000-0000-000000000000"
	tenantB      = "bbbbbbbb-0000-0000-0000-000000000000"
)

var (
	adminA    = auth.Principal{UserID: "admin-a", TenantID: tenantA, Role: auth.RoleAdmin}
	adminB    = auth.Principal{UserID: "admin-b", TenantID: tenantB, Role: auth.RoleAdmin}
	employeeA = auth.Principal{UserID: "user-e1", TenantID: tenantA, Role: auth.RoleEmployee}
)

type memoryRepository struct {
	employees  map[string]*Employee
	logins     map[string]*auth.User
	lastFilter ListFilter
	deletedAt  map[string]time.Time
	updateErr  error
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		employees: map[string]*Employee{
			"e1": {ID: "e1", TenantID: tenantA, UserID: ptr("user-e1"), EmployeeCode: "EMP-0001", FullName: "Somchai", Email: "somchai@example.test", Status: StatusActive, Version: 1},
			"e2": {ID: "e2", TenantID: tenantA, UserID: ptr("user-e2"), EmployeeCode: "EMP-0002", FullName: "Other", Email: "other@example.test", Status: StatusActive, Version: 1},
		},
		logins:    map[string]*auth.User{},
		deletedAt: map[string]time.Time{},
	}
}

func (r *memoryRepository) List(_ context.Context, filter ListFilter) ([]*Employee, int64, error) {
	r.lastFilter = filter
	var result []*Employee
	for _, e := range r.employees {
		if e.TenantID == filter.TenantID {
			result = append(result, e)
		}
	}
	return result, int64(len(result)), nil
}

func (r *memoryRepository) FindByID(_ context.Context, tenantID, id string) (*Employee, error) {
	e, ok := r.employees[id]
	if !ok || e.TenantID != tenantID {
		return nil, errs.NotFound("Employee not found.")
	}
	copied := *e
	return &copied, nil
}

func (r *memoryRepository) DepartmentExists(_ context.Context, id string) (bool, error) {
	return id == departmentIT, nil
}

func (r *memoryRepository) Create(_ context.Context, e *Employee, login *auth.User, _ int) error {
	login.ID = "new-user"
	e.ID, e.UserID, e.EmployeeCode, e.Version = "new", &login.ID, "EMP-0003", 1
	r.logins[login.ID] = login
	r.employees[e.ID] = e
	return nil
}

func (r *memoryRepository) Update(_ context.Context, e *Employee, expectedVersion int) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	if r.employees[e.ID].Version != expectedVersion {
		return apierror.VersionConflict
	}
	e.Version++
	r.employees[e.ID] = e
	return nil
}

func (r *memoryRepository) Delete(_ context.Context, tenantID, id string, now time.Time) error {
	if _, err := r.FindByID(context.Background(), tenantID, id); err != nil {
		return err
	}
	r.deletedAt[id] = now
	delete(r.employees, id)
	return nil
}

func (r *memoryRepository) Departments(context.Context) ([]Department, error) {
	return []Department{{ID: departmentIT, Name: "IT"}}, nil
}

func errorCode(err error) string {
	if appErr, ok := errs.IsAppError(err); ok {
		return appErr.Code
	}
	return ""
}

func detailFields(t *testing.T, err error) map[string]bool {
	t.Helper()
	appErr, ok := errs.IsAppError(err)
	if !ok || appErr.Code != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %v", err)
	}
	fields := map[string]bool{}
	for _, detail := range appErr.Data.([]apierror.FieldError) {
		fields[detail.Field] = true
	}
	return fields
}

func ptr[T any](value T) *T { return &value }

func patch(body string) UpdateEmployeeInput {
	var input UpdateEmployeeInput
	if err := json.Unmarshal([]byte(body), &input); err != nil {
		panic(err)
	}
	return input
}

func TestCreateMakesEmployeeLoginInCallerTenant(t *testing.T) {
	repo := newMemoryRepository()
	uc := NewEmployeeUsecase(repo, nil, "")
	created, err := uc.Create(context.Background(), adminA, CreateEmployeeInput{
		FullName: " Somsak Example ", Email: "SOMSAK@example.test", Password: "password-123",
		Phone: ptr("020 5555 5555"), DepartmentID: ptr(departmentIT), Position: ptr("Developer"), HireDate: ptr("2024-03-01"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	login := repo.logins["new-user"]
	if login.Role != auth.RoleEmployee || login.TenantID != tenantA || !login.Active || login.Email != "somsak@example.test" {
		t.Fatalf("unexpected login %+v", login)
	}
	if bcrypt.CompareHashAndPassword([]byte(login.PasswordHash), []byte("password-123")) != nil {
		t.Fatal("password was not stored as a bcrypt hash")
	}
	if created.FullName != "Somsak Example" || created.Status != StatusActive || *created.HireDate != "2024-03-01" || created.EmployeeCode != "EMP-0003" {
		t.Fatalf("unexpected view %+v", created)
	}
}

func TestCreateReportsEveryInvalidField(t *testing.T) {
	uc := NewEmployeeUsecase(newMemoryRepository(), nil, "")
	_, err := uc.Create(context.Background(), adminA, CreateEmployeeInput{
		FullName: "A", Email: "not-an-email", Password: "ກຂຄ", Phone: ptr("call me"),
		DepartmentID: ptr("22222222-2222-2222-2222-222222222222"), Position: ptr(string(make([]byte, 101))),
		Status: "fired", HireDate: ptr(time.Now().UTC().AddDate(0, 0, 2).Format(time.DateOnly)),
	})
	fields := detailFields(t, err)
	for _, field := range []string{"fullName", "email", "password", "phone", "departmentId", "position", "status", "hireDate"} {
		if !fields[field] {
			t.Errorf("missing validation detail for %s in %v", field, fields)
		}
	}
}

func TestOnlyAdminsListCreateAndDelete(t *testing.T) {
	uc := NewEmployeeUsecase(newMemoryRepository(), nil, "")
	ctx := context.Background()
	if _, _, err := uc.List(ctx, employeeA, ListQuery{}); errorCode(err) != "FORBIDDEN" {
		t.Errorf("employee list: got %v", err)
	}
	if _, err := uc.Create(ctx, employeeA, CreateEmployeeInput{}); errorCode(err) != "FORBIDDEN" {
		t.Errorf("employee create: got %v", err)
	}
	if err := uc.Delete(ctx, employeeA, "e1"); errorCode(err) != "FORBIDDEN" {
		t.Errorf("employee delete: got %v", err)
	}
}

func TestOtherTenantAdminCannotSeeOrChangeEmployee(t *testing.T) {
	repo := newMemoryRepository()
	uc := NewEmployeeUsecase(repo, nil, "")
	ctx := context.Background()
	if _, err := uc.Get(ctx, adminB, "e1"); errorCode(err) != "NOT_FOUND" {
		t.Errorf("get: got %v", err)
	}
	if _, err := uc.Update(ctx, adminB, "e1", patch(`{"version":1,"fullName":"Hijacked"}`)); errorCode(err) != "NOT_FOUND" {
		t.Errorf("update: got %v", err)
	}
	if err := uc.Delete(ctx, adminB, "e1"); errorCode(err) != "NOT_FOUND" {
		t.Errorf("delete: got %v", err)
	}
	if _, _, err := uc.List(ctx, adminB, ListQuery{}); err != nil || repo.lastFilter.TenantID != tenantB {
		t.Errorf("list must be scoped to the caller tenant, filter %+v err %v", repo.lastFilter, err)
	}
	if repo.employees["e1"].FullName != "Somchai" {
		t.Fatal("cross-tenant update changed the Employee")
	}
}

func TestEmployeeReadsOnlyOwnProfile(t *testing.T) {
	uc := NewEmployeeUsecase(newMemoryRepository(), nil, "")
	ctx := context.Background()
	if self, err := uc.Get(ctx, employeeA, "e1"); err != nil || self.ID != "e1" {
		t.Fatalf("self read: %v %v", self, err)
	}
	for _, id := range []string{"e2", "missing"} {
		if _, err := uc.Get(ctx, employeeA, id); errorCode(err) != "FORBIDDEN" {
			t.Errorf("read %s: got %v", id, err)
		}
	}
}

func TestPatchEnforcesRoleWritableFields(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		actor auth.Principal
		id    string
		body  string
		code  string
	}{
		{"employee updates own profile fields", employeeA, "e1", `{"version":1,"fullName":"New Name","phone":null}`, ""},
		{"employee cannot change status", employeeA, "e1", `{"version":1,"status":"inactive"}`, "FORBIDDEN"},
		{"employee cannot change email", employeeA, "e1", `{"version":1,"email":"x@example.test"}`, "FORBIDDEN"},
		{"employee cannot patch another employee", employeeA, "e2", `{"version":1,"fullName":"Other Name"}`, "FORBIDDEN"},
		{"admin updates administrative fields", adminA, "e2", `{"version":1,"status":"on_leave","departmentId":"` + departmentIT + `","hireDate":"2024-01-31","position":null}`, ""},
		{"admin cannot set avatarUrl", adminA, "e2", `{"version":1,"avatarUrl":null}`, "FORBIDDEN"},
		{"employee cannot set avatarUrl", employeeA, "e1", `{"version":1,"avatarUrl":"https://cdn.example.test/a.png"}`, "FORBIDDEN"},
		{"nobody changes role", adminA, "e2", `{"version":1,"role":"admin"}`, "FORBIDDEN"},
		{"version is required", adminA, "e2", `{"fullName":"Valid Name"}`, "VALIDATION_ERROR"},
		{"stale version conflicts", adminA, "e2", `{"version":2,"fullName":"Valid Name"}`, "VERSION_CONFLICT"},
		{"invalid employee values are rejected", employeeA, "e1", `{"version":1,"fullName":null}`, "VALIDATION_ERROR"},
		{"invalid admin values are rejected", adminA, "e2", `{"version":1,"fullName":"","email":"bad","status":"gone"}`, "VALIDATION_ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newMemoryRepository()
			updated, err := NewEmployeeUsecase(repo, nil, "").Update(ctx, tc.actor, tc.id, patch(tc.body))
			if errorCode(err) != tc.code {
				t.Fatalf("got error %v, want code %q", err, tc.code)
			}
			if tc.code != "" {
				if repo.employees[tc.id].Version != 1 {
					t.Fatal("rejected patch was persisted")
				}
				return
			}
			if updated.Version != 2 {
				t.Fatalf("version = %d, want 2", updated.Version)
			}
		})
	}
}

func TestPatchAppliesOnlyProvidedFields(t *testing.T) {
	repo := newMemoryRepository()
	repo.employees["e1"].Phone = ptr("020 1111 1111")
	repo.employees["e1"].Position = ptr("Developer")
	updated, err := NewEmployeeUsecase(repo, nil, "").Update(context.Background(), employeeA, "e1", patch(`{"version":1,"phone":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Phone != nil || updated.Position == nil || *updated.Position != "Developer" || updated.FullName != "Somchai" {
		t.Fatalf("unexpected patch result %+v", updated)
	}
}

func TestListNormalizesQuery(t *testing.T) {
	repo := newMemoryRepository()
	uc := NewEmployeeUsecase(repo, nil, "")
	_, meta, err := uc.List(context.Background(), adminA, ListQuery{
		Query: pagination.Query{Page: 0, Limit: 500, Search: "  Som  "}, DepartmentID: departmentIT,
		Status: "active, on_leave,active", SortBy: "fullName", SortOrder: "desc",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := ListFilter{TenantID: tenantA, Search: "Som", DepartmentID: departmentIT, Statuses: []Status{StatusActive, StatusOnLeave}, SortBy: SortFullName, Desc: true, Offset: 0, Limit: 100}
	got := repo.lastFilter
	if got.TenantID != want.TenantID || got.Search != want.Search || got.DepartmentID != want.DepartmentID || got.SortBy != want.SortBy ||
		got.Desc != want.Desc || got.Offset != want.Offset || got.Limit != want.Limit || len(got.Statuses) != 2 || got.Statuses[1] != StatusOnLeave {
		t.Fatalf("filter = %+v, want %+v", got, want)
	}
	if meta.Page != 1 || meta.Limit != 100 || meta.Total != 2 || meta.TotalPages != 1 {
		t.Fatalf("meta = %+v", meta)
	}
	if _, _, err := uc.List(context.Background(), adminA, ListQuery{Query: pagination.Query{Page: 3, Limit: 10}}); err != nil || repo.lastFilter.Offset != 20 || repo.lastFilter.SortBy != SortCreatedAt {
		t.Fatalf("defaults: filter %+v err %v", repo.lastFilter, err)
	}
	_, _, err = uc.List(context.Background(), adminA, ListQuery{Status: "active,fired", SortBy: "password", SortOrder: "up", DepartmentID: "IT"})
	fields := detailFields(t, err)
	for _, field := range []string{"status", "sortBy", "sortOrder", "departmentId"} {
		if !fields[field] {
			t.Errorf("missing validation detail for %s", field)
		}
	}
}

const avatarBucket = "candidate-api-files"

var (
	jpegMagic = []byte{0xFF, 0xD8, 0xFF}
	pngMagic  = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
)

type memoryStorage struct {
	objects      map[string][]byte
	contentTypes map[string]string
	failUpload   bool
	failDelete   bool
	failURL      bool
	urlCalls     []string
	expiry       time.Duration
}

func newMemoryStorage() *memoryStorage {
	return &memoryStorage{objects: map[string][]byte{}, contentTypes: map[string]string{}}
}

func (s *memoryStorage) objectKey(bucket, name string) string { return bucket + "/" + name }

func (s *memoryStorage) Upload(_ context.Context, bucket, name string, reader io.Reader, _ int64, contentType string) (string, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	if s.failUpload {
		return "", errors.New("storage unavailable")
	}
	key := s.objectKey(bucket, name)
	s.objects[key] = data
	s.contentTypes[key] = contentType
	return key, nil
}

func (s *memoryStorage) Download(_ context.Context, bucket, name string) (io.ReadCloser, error) {
	data, ok := s.objects[s.objectKey(bucket, name)]
	if !ok {
		return nil, errors.New("missing")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *memoryStorage) Delete(_ context.Context, bucket, name string) error {
	if s.failDelete {
		return errors.New("delete failed")
	}
	delete(s.objects, s.objectKey(bucket, name))
	return nil
}

func (s *memoryStorage) URL(_ context.Context, bucket, name string, expiry time.Duration) (string, error) {
	s.urlCalls = append(s.urlCalls, name)
	s.expiry = expiry
	if s.failURL {
		return "", errors.New("presign failed")
	}
	return "https://files.example.test/" + s.objectKey(bucket, name) + "?X-Amz-Expires=900", nil
}

var _ contract.Storage = (*memoryStorage)(nil)

func imageBytes(n int, magic []byte) []byte {
	if n < len(magic) {
		n = len(magic)
	}
	data := bytes.Repeat([]byte{0x11}, n)
	copy(data, magic)
	return data
}

func webpBytes() []byte {
	data := bytes.Repeat([]byte{0x11}, 16)
	copy(data, []byte("RIFF"))
	copy(data[8:], []byte("WEBP"))
	return data
}

func avatarFile(contentType string, data []byte) AvatarFile {
	return AvatarFile{ContentType: contentType, Size: int64(len(data)), Body: bytes.NewReader(data)}
}

func TestUploadAvatarStoresPrivateObjectAndReturnsPresignedURL(t *testing.T) {
	repo := newMemoryRepository()
	files := newMemoryStorage()
	payload := imageBytes(32, jpegMagic)
	view, err := NewEmployeeUsecase(repo, files, avatarBucket).UploadAvatar(context.Background(), employeeA, "e1", avatarFile("image/jpeg", payload))
	if err != nil {
		t.Fatal(err)
	}
	stored := repo.employees["e1"].AvatarURL
	if stored == nil || view.AvatarURL == nil || *stored == *view.AvatarURL {
		t.Fatalf("stored %v view %v", stored, view.AvatarURL)
	}
	if !strings.HasPrefix(*stored, "files/avatars/"+tenantA+"/e1/") || !strings.HasSuffix(*stored, ".jpg") {
		t.Fatalf("object key = %s", *stored)
	}
	if *view.AvatarURL != "https://files.example.test/"+avatarBucket+"/"+*stored+"?X-Amz-Expires=900" {
		t.Fatalf("avatarUrl = %s", *view.AvatarURL)
	}
	if !bytes.Equal(files.objects[avatarBucket+"/"+*stored], payload) {
		t.Fatal("stored object bytes differ from the upload")
	}
	if files.contentTypes[avatarBucket+"/"+*stored] != "image/jpeg" {
		t.Fatalf("content type = %s", files.contentTypes[avatarBucket+"/"+*stored])
	}
	if files.expiry <= 0 || files.expiry > 15*time.Minute {
		t.Fatalf("presign expiry = %s", files.expiry)
	}
	if view.Version != 2 {
		t.Fatalf("version = %d", view.Version)
	}
}

func TestUploadAvatarAcceptsPNGAndWebP(t *testing.T) {
	cases := []struct {
		contentType string
		data        []byte
		ext         string
		storedType  string
	}{
		{"image/png", imageBytes(16, pngMagic), ".png", "image/png"},
		{"image/webp", webpBytes(), ".webp", "image/webp"},
		{"image/jpg", imageBytes(16, jpegMagic), ".jpg", "image/jpeg"},
		{"image/jpeg; charset=binary", imageBytes(16, jpegMagic), ".jpg", "image/jpeg"},
	}
	for _, tc := range cases {
		t.Run(tc.contentType, func(t *testing.T) {
			repo := newMemoryRepository()
			files := newMemoryStorage()
			view, err := NewEmployeeUsecase(repo, files, avatarBucket).UploadAvatar(context.Background(), adminA, "e2", avatarFile(tc.contentType, tc.data))
			if err != nil {
				t.Fatal(err)
			}
			stored := *repo.employees["e2"].AvatarURL
			if !strings.HasSuffix(stored, tc.ext) || files.contentTypes[avatarBucket+"/"+stored] != tc.storedType || view.AvatarURL == nil {
				t.Fatalf("stored %s type %s view %v", stored, files.contentTypes[avatarBucket+"/"+stored], view.AvatarURL)
			}
		})
	}
}

func TestUploadAvatarRejectsInvalidFiles(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		data        []byte
		size        int64
		message     string
	}{
		{"gif", "image/gif", []byte("GIF89a"), int64(len("GIF89a")), msgAvatarType},
		{"declared jpeg with png bytes", "image/jpeg", imageBytes(16, pngMagic), 16, msgAvatarType},
		{"empty", "image/jpeg", nil, 0, msgAvatarType},
		{"svg", "image/svg+xml", []byte("<svg></svg>"), int64(len("<svg></svg>")), msgAvatarType},
		{"too large", "image/jpeg", imageBytes(maxAvatarBytes+1, jpegMagic), -1, msgAvatarSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newMemoryRepository()
			files := newMemoryStorage()
			file := AvatarFile{ContentType: tc.contentType, Size: tc.size, Body: bytes.NewReader(tc.data)}
			_, err := NewEmployeeUsecase(repo, files, avatarBucket).UploadAvatar(context.Background(), employeeA, "e1", file)
			fields := detailFields(t, err)
			if !fields["file"] {
				t.Fatalf("fields = %v", fields)
			}
			if len(files.objects) != 0 || repo.employees["e1"].AvatarURL != nil || repo.employees["e1"].Version != 1 {
				t.Fatal("rejected upload was stored")
			}
			appErr, _ := errs.IsAppError(err)
			if appErr.Data.([]apierror.FieldError)[0].Message != tc.message {
				t.Fatalf("message = %s", appErr.Data.([]apierror.FieldError)[0].Message)
			}
		})
	}
	repo := newMemoryRepository()
	files := newMemoryStorage()
	payload := imageBytes(maxAvatarBytes, jpegMagic)
	if _, err := NewEmployeeUsecase(repo, files, avatarBucket).UploadAvatar(context.Background(), employeeA, "e1", AvatarFile{
		ContentType: "image/jpeg", Size: -1, Body: bytes.NewReader(payload),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestUploadAvatarAuthorization(t *testing.T) {
	ctx := context.Background()
	file := func() AvatarFile { return avatarFile("image/png", imageBytes(16, pngMagic)) }
	cases := []struct {
		name  string
		actor auth.Principal
		id    string
		code  string
	}{
		{"employee uploads own avatar", employeeA, "e1", ""},
		{"admin uploads tenant employee avatar", adminA, "e2", ""},
		{"employee cannot upload for someone else", employeeA, "e2", "FORBIDDEN"},
		{"other tenant cannot upload", adminB, "e1", "NOT_FOUND"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newMemoryRepository()
			files := newMemoryStorage()
			_, err := NewEmployeeUsecase(repo, files, avatarBucket).UploadAvatar(ctx, tc.actor, tc.id, file())
			if errorCode(err) != tc.code {
				t.Fatalf("got %v, want %q", err, tc.code)
			}
			if tc.code != "" && (len(files.objects) != 0 || repo.employees["e1"].AvatarURL != nil) {
				t.Fatal("unauthorized upload was stored")
			}
		})
	}
}

func uploadJPEGAvatar(repo EmployeeRepository, files contract.Storage) error {
	_, err := NewEmployeeUsecase(repo, files, avatarBucket).UploadAvatar(context.Background(), employeeA, "e1", avatarFile("image/jpeg", imageBytes(16, jpegMagic)))
	return err
}

func TestUploadAvatarUploadFailureLeavesNoAvatar(t *testing.T) {
	repo := newMemoryRepository()
	files := newMemoryStorage()
	files.failUpload = true
	err := uploadJPEGAvatar(repo, files)
	if errorCode(err) != "INTERNAL_ERROR" || strings.Contains(err.Error(), "storage unavailable") {
		t.Fatalf("err = %v", err)
	}
	if repo.employees["e1"].AvatarURL != nil || repo.employees["e1"].Version != 1 || len(files.objects) != 0 {
		t.Fatal("failed upload changed the Employee or left an object")
	}
}

func TestUploadAvatarMetadataFailureRemovesNewObject(t *testing.T) {
	repo := newMemoryRepository()
	repo.updateErr = errors.New("db down")
	files := newMemoryStorage()
	err := uploadJPEGAvatar(repo, files)
	if err == nil || len(files.objects) != 0 || repo.employees["e1"].AvatarURL != nil {
		t.Fatalf("err %v objects %d", err, len(files.objects))
	}
}

func TestUploadAvatarPresignFailureKeepsStoredObject(t *testing.T) {
	repo := newMemoryRepository()
	files := newMemoryStorage()
	files.failURL = true
	err := uploadJPEGAvatar(repo, files)
	if errorCode(err) != "INTERNAL_ERROR" || repo.employees["e1"].AvatarURL == nil || len(files.objects) != 1 {
		t.Fatalf("err %v stored %v objects %d", err, repo.employees["e1"].AvatarURL, len(files.objects))
	}
	if strings.Contains(err.Error(), *repo.employees["e1"].AvatarURL) {
		t.Fatal("error exposed the object key")
	}
}

func TestUploadAvatarRequiresStorage(t *testing.T) {
	if err := uploadJPEGAvatar(newMemoryRepository(), nil); errorCode(err) != "INTERNAL_ERROR" {
		t.Fatalf("err = %v", err)
	}
}

func TestUploadAvatarReplacesPreviousObject(t *testing.T) {
	repo := newMemoryRepository()
	files := newMemoryStorage()
	uc := NewEmployeeUsecase(repo, files, avatarBucket)
	ctx := context.Background()
	first, err := uc.UploadAvatar(ctx, employeeA, "e1", avatarFile("image/jpeg", imageBytes(16, jpegMagic)))
	if err != nil {
		t.Fatal(err)
	}
	oldKey := *repo.employees["e1"].AvatarURL
	second, err := uc.UploadAvatar(ctx, employeeA, "e1", avatarFile("image/png", imageBytes(16, pngMagic)))
	if err != nil {
		t.Fatal(err)
	}
	newKey := *repo.employees["e1"].AvatarURL
	if _, ok := files.objects[avatarBucket+"/"+oldKey]; ok || files.objects[avatarBucket+"/"+newKey] == nil {
		t.Fatalf("old present=%v new present=%v", files.objects[avatarBucket+"/"+oldKey] != nil, files.objects[avatarBucket+"/"+newKey] != nil)
	}
	if first.AvatarURL == nil || second.AvatarURL == nil || *first.AvatarURL == *second.AvatarURL || second.Version != 3 {
		t.Fatalf("first %v second %v version %d", first.AvatarURL, second.AvatarURL, second.Version)
	}
}

func TestPreviousAvatarDeleteFailureKeepsNewObject(t *testing.T) {
	repo := newMemoryRepository()
	files := newMemoryStorage()
	uc := NewEmployeeUsecase(repo, files, avatarBucket)
	ctx := context.Background()
	if _, err := uc.UploadAvatar(ctx, adminA, "e1", avatarFile("image/jpeg", imageBytes(16, jpegMagic))); err != nil {
		t.Fatal(err)
	}
	oldKey := *repo.employees["e1"].AvatarURL
	files.failDelete = true
	view, err := uc.UploadAvatar(ctx, adminA, "e1", avatarFile("image/png", imageBytes(16, pngMagic)))
	if err != nil {
		t.Fatal(err)
	}
	newKey := *repo.employees["e1"].AvatarURL
	if view.AvatarURL == nil || !strings.Contains(*view.AvatarURL, newKey) {
		t.Fatalf("view = %v", view.AvatarURL)
	}
	if files.objects[avatarBucket+"/"+oldKey] == nil || files.objects[avatarBucket+"/"+newKey] == nil {
		t.Fatal("both objects should remain when deleting the previous one fails")
	}
}

func TestAvatarRetrievalRequiresAuthorization(t *testing.T) {
	repo := newMemoryRepository()
	key := "files/avatars/" + tenantA + "/e1/existing.jpg"
	repo.employees["e1"].AvatarURL = &key
	files := newMemoryStorage()
	files.objects[avatarBucket+"/"+key] = imageBytes(16, jpegMagic)
	uc := NewEmployeeUsecase(repo, files, avatarBucket)
	ctx := context.Background()

	self, err := uc.Get(ctx, employeeA, "e1")
	if err != nil || self.AvatarURL == nil || *self.AvatarURL == key {
		t.Fatalf("self = %+v err %v", self, err)
	}
	listed, _, err := uc.List(ctx, adminA, ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var listedURL *string
	for _, employee := range listed {
		if employee.ID == "e1" {
			listedURL = employee.AvatarURL
		}
		if employee.ID == "e2" && employee.AvatarURL != nil {
			t.Fatal("employee without an avatar received a url")
		}
	}
	if listedURL == nil || *listedURL == key {
		t.Fatalf("list url = %v", listedURL)
	}

	files.urlCalls = nil
	if _, err := uc.Get(ctx, employeeA, "e2"); errorCode(err) != "FORBIDDEN" || len(files.urlCalls) != 0 {
		t.Fatalf("other employee get err %v urls %v", err, files.urlCalls)
	}
	if _, err := uc.Get(ctx, adminB, "e1"); errorCode(err) != "NOT_FOUND" || len(files.urlCalls) != 0 {
		t.Fatalf("other tenant get err %v urls %v", err, files.urlCalls)
	}
}

func TestAvatarKeyOutsideTenantIsNotPresigned(t *testing.T) {
	repo := newMemoryRepository()
	key := "files/avatars/" + tenantB + "/e1/other.jpg"
	repo.employees["e1"].AvatarURL = &key
	files := newMemoryStorage()
	view, err := NewEmployeeUsecase(repo, files, avatarBucket).Get(context.Background(), adminA, "e1")
	if err != nil || view.AvatarURL != nil || len(files.urlCalls) != 0 {
		t.Fatalf("view %+v err %v calls %v", view, err, files.urlCalls)
	}
}

func TestAvatarURLFailureDoesNotLeakObjectKey(t *testing.T) {
	repo := newMemoryRepository()
	key := "files/avatars/" + tenantA + "/e1/existing.jpg"
	repo.employees["e1"].AvatarURL = &key
	files := newMemoryStorage()
	files.failURL = true
	_, err := NewEmployeeUsecase(repo, files, avatarBucket).Get(context.Background(), adminA, "e1")
	if errorCode(err) != "INTERNAL_ERROR" || strings.Contains(err.Error(), key) {
		t.Fatalf("err = %v", err)
	}
}

func TestProfileUpdateKeepsAvatarObject(t *testing.T) {
	repo := newMemoryRepository()
	key := "files/avatars/" + tenantA + "/e1/keep.jpg"
	repo.employees["e1"].AvatarURL = &key
	files := newMemoryStorage()
	updated, err := NewEmployeeUsecase(repo, files, avatarBucket).Update(context.Background(), employeeA, "e1", patch(`{"version":1,"phone":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if repo.employees["e1"].AvatarURL == nil || *repo.employees["e1"].AvatarURL != key {
		t.Fatalf("stored key = %v", repo.employees["e1"].AvatarURL)
	}
	if updated.AvatarURL == nil || strings.HasPrefix(*updated.AvatarURL, "files/avatars/") {
		t.Fatalf("view = %v", updated.AvatarURL)
	}
}

func TestDeactivateKeepsAvatarObject(t *testing.T) {
	repo := newMemoryRepository()
	files := newMemoryStorage()
	uc := NewEmployeeUsecase(repo, files, avatarBucket)
	if _, err := uc.UploadAvatar(context.Background(), employeeA, "e1", avatarFile("image/jpeg", imageBytes(16, jpegMagic))); err != nil {
		t.Fatal(err)
	}
	key := *repo.employees["e1"].AvatarURL
	if err := uc.Delete(context.Background(), adminA, "e1"); err != nil {
		t.Fatal(err)
	}
	if files.objects[avatarBucket+"/"+key] == nil {
		t.Fatal("deactivating an Employee removed the private avatar")
	}
}

func TestDeleteRemovesTenantEmployee(t *testing.T) {
	repo := newMemoryRepository()
	if err := NewEmployeeUsecase(repo, nil, "").Delete(context.Background(), adminA, "e1"); err != nil {
		t.Fatal(err)
	}
	if _, deleted := repo.deletedAt["e1"]; !deleted {
		t.Fatal("repository delete was not called")
	}
}
