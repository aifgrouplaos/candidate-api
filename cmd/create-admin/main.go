package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"os"
	"strings"

	gormadapter "github.com/BounkhongDev/bkgo/adapter/gorm"
	"github.com/BounkhongDev/bkgo/config"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
	"gorm.io/gorm"
)

const (
	minPasswordLength = 8
	maxPasswordLength = 72 // bcrypt ignores bytes after this limit.
	maxNameLength     = 100
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(input io.Reader, output io.Writer) error {
	stdin, ok := input.(*os.File)
	if !ok || !term.IsTerminal(int(stdin.Fd())) {
		return errors.New("create-admin requires an interactive terminal for password entry")
	}

	reader := bufio.NewReader(input)
	email, err := promptLine(reader, output, "Admin email: ")
	if err != nil {
		return err
	}
	email, err = normalizeEmail(email)
	if err != nil {
		return err
	}

	fullName, err := promptLine(reader, output, "Admin name: ")
	if err != nil {
		return err
	}
	fullName = strings.TrimSpace(fullName)
	if len([]rune(fullName)) < 2 || len([]rune(fullName)) > maxNameLength {
		return fmt.Errorf("admin name must be between 2 and %d characters", maxNameLength)
	}

	password, err := promptPassword(stdin, output, "Password: ")
	if err != nil {
		return err
	}
	confirmation, err := promptPassword(stdin, output, "Confirm password: ")
	if err != nil {
		return err
	}
	if string(password) != string(confirmation) {
		return errors.New("passwords do not match")
	}
	if len(password) < minPasswordLength || len(password) > maxPasswordLength {
		return fmt.Errorf("password must be between %d and %d bytes", minPasswordLength, maxPasswordLength)
	}

	passwordHash, err := bcrypt.GenerateFromPassword(password, bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("could not hash password: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config load failed: %w", err)
	}
	if !cfg.PostgresEnabled {
		return errors.New("PostgreSQL is disabled; set DB_ENABLED=true")
	}

	db, err := gormadapter.New(cfg.Postgres)
	if err != nil {
		return fmt.Errorf("postgres connect failed: %w", err)
	}
	defer db.Close()

	if err := db.Raw().AutoMigrate(&auth.User{}); err != nil {
		return fmt.Errorf("user schema migration failed: %w", err)
	}

	tenantID := uuid.NewString()
	user := auth.User{
		ID:           uuid.NewString(),
		TenantID:     tenantID,
		Email:        email,
		PasswordHash: string(passwordHash),
		Role:         auth.RoleAdmin,
		FullName:     fullName,
		Active:       true,
	}
	if err := createAdmin(context.Background(), db.Raw(), &user); err != nil {
		return err
	}

	fmt.Fprintf(output, "Created admin %s\nTenant ID: %s\n", user.Email, user.TenantID)
	return nil
}

func promptLine(reader *bufio.Reader, output io.Writer, label string) (string, error) {
	fmt.Fprint(output, label)
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if len(value) == 0 && errors.Is(err, io.EOF) {
		return "", errors.New("input ended before all account details were entered")
	}
	return strings.TrimRight(value, "\r\n"), nil
}

func promptPassword(stdin *os.File, output io.Writer, label string) ([]byte, error) {
	fmt.Fprint(output, label)
	password, err := term.ReadPassword(int(stdin.Fd()))
	fmt.Fprintln(output)
	if err != nil {
		return nil, fmt.Errorf("could not read password: %w", err)
	}
	return password, nil
}

func normalizeEmail(value string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(value))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return "", errors.New("enter a valid email address")
	}
	return email, nil
}

func createAdmin(ctx context.Context, db *gorm.DB, user *auth.User) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&auth.User{}).Where("LOWER(email) = ?", user.Email).Count(&count).Error; err != nil {
			return fmt.Errorf("could not check email uniqueness: %w", err)
		}
		if count != 0 {
			return fmt.Errorf("email %s is already in use; existing accounts are not changed", user.Email)
		}
		if err := tx.Create(user).Error; err != nil {
			var sqlState interface{ SQLState() string }
			if errors.As(err, &sqlState) && sqlState.SQLState() == "23505" {
				return fmt.Errorf("email %s is already in use; existing accounts are not changed", user.Email)
			}
			return fmt.Errorf("admin creation failed: %w", err)
		}
		return nil
	})
}
