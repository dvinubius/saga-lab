package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const AdminURLVariable = "SAGAS_POSTGRES_URL"

func NewDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	adminURL, admin := connectAdmin(t)
	ctx := context.Background()

	name := "test_" + randomSuffix(t)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop test database: %v", err)
		}
	})

	config, err := pgxpool.ParseConfig(adminURL)
	if err != nil {
		t.Fatalf("parse %s: %v", AdminURLVariable, err)
	}
	config.ConnConfig.Database = name
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func NewServiceDatabase(t *testing.T, name string) string {
	t.Helper()
	adminURL, admin := connectAdmin(t)
	ctx := context.Background()

	if _, err := admin.Exec(ctx, "CREATE ROLE "+name+" LOGIN PASSWORD '"+name+"'"); err != nil {
		t.Fatalf("create role %s: %v", name, err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "DROP ROLE "+name); err != nil {
			t.Errorf("drop role %s: %v", name, err)
		}
	})
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name+" OWNER "+name); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop database %s: %v", name, err)
		}
	})
	if _, err := admin.Exec(ctx, "REVOKE ALL ON DATABASE "+name+" FROM PUBLIC"); err != nil {
		t.Fatalf("revoke access to database %s: %v", name, err)
	}

	serviceURL, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse %s: %v", AdminURLVariable, err)
	}
	serviceURL.User = url.UserPassword(name, name)
	serviceURL.Path = "/" + name
	return serviceURL.String()
}

func connectAdmin(t *testing.T) (string, *pgx.Conn) {
	t.Helper()
	adminURL := os.Getenv(AdminURLVariable)
	if adminURL == "" {
		t.Skipf("%s is not set; run scripts/test.sh", AdminURLVariable)
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	t.Cleanup(func() { admin.Close(ctx) })
	return adminURL, admin
}

func randomSuffix(t *testing.T) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("generate database name: %v", err)
	}
	return hex.EncodeToString(b)
}
