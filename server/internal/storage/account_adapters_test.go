package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestMySQLPlayerProfileRepositoryCreateAndLookup(t *testing.T) {
	exec := newMemorySQLExecutor()
	repo, err := NewMySQLPlayerProfileRepository(exec)
	if err != nil {
		t.Fatalf("NewMySQLPlayerProfileRepository() error = %v", err)
	}
	now := time.UnixMilli(123456789)
	profile := PlayerProfile{
		PlayerID:     "player-1",
		AccountName:  "tester",
		PasswordHash: "hash",
		DisplayName:  "Tester",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := repo.CreatePlayerProfile(context.Background(), profile); err != nil {
		t.Fatalf("CreatePlayerProfile() error = %v", err)
	}
	if err := repo.CreatePlayerProfile(context.Background(), profile); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate CreatePlayerProfile() error = %v, want ErrConflict", err)
	}

	got, err := repo.GetPlayerProfileByAccount(context.Background(), "tester")
	if err != nil {
		t.Fatalf("GetPlayerProfileByAccount() error = %v", err)
	}
	if !reflect.DeepEqual(got, profile) {
		t.Fatalf("profile by account = %+v, want %+v", got, profile)
	}
	got, err = repo.GetPlayerProfileByID(context.Background(), "player-1")
	if err != nil {
		t.Fatalf("GetPlayerProfileByID() error = %v", err)
	}
	if got.AccountName != "tester" {
		t.Fatalf("AccountName = %q, want tester", got.AccountName)
	}
	if _, err := repo.GetPlayerProfileByAccount(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing account error = %v, want ErrNotFound", err)
	}
}

func TestRedisAccountSessionCacheSetGetDelete(t *testing.T) {
	keys, err := NewRedisKeys("dev")
	if err != nil {
		t.Fatalf("NewRedisKeys() error = %v", err)
	}
	client := newMemoryRedisClient()
	cache, err := NewRedisAccountSessionCache(client, keys)
	if err != nil {
		t.Fatalf("NewRedisAccountSessionCache() error = %v", err)
	}
	session := AccountSession{
		SessionToken: "token-1",
		PlayerID:     "player-1",
		AccountName:  "tester",
		IssuedAt:     time.UnixMilli(1000),
		ExpiresAt:    time.UnixMilli(2000),
		ConnectionID: "conn-1",
	}

	if err := cache.SetAccountSession(context.Background(), session, time.Minute); err != nil {
		t.Fatalf("SetAccountSession() error = %v", err)
	}
	key, _ := keys.AccountSession("token-1")
	if client.ttls[key] != time.Minute {
		t.Fatalf("ttl = %s, want 1m", client.ttls[key])
	}
	got, err := cache.GetAccountSession(context.Background(), "token-1")
	if err != nil {
		t.Fatalf("GetAccountSession() error = %v", err)
	}
	if !reflect.DeepEqual(got, session) {
		t.Fatalf("session = %+v, want %+v", got, session)
	}
	if err := cache.DeleteAccountSession(context.Background(), "token-1"); err != nil {
		t.Fatalf("DeleteAccountSession() error = %v", err)
	}
	if _, err := cache.GetAccountSession(context.Background(), "token-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetAccountSession() after delete error = %v, want ErrNotFound", err)
	}
}

type memorySQLExecutor struct {
	byAccount map[string]PlayerProfile
	byID      map[string]PlayerProfile
}

func newMemorySQLExecutor() *memorySQLExecutor {
	return &memorySQLExecutor{
		byAccount: make(map[string]PlayerProfile),
		byID:      make(map[string]PlayerProfile),
	}
}

func (e *memorySQLExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	profile := PlayerProfile{
		PlayerID:     args[0].(string),
		AccountName:  args[1].(string),
		PasswordHash: args[2].(string),
		DisplayName:  args[3].(string),
		CreatedAt:    time.UnixMilli(args[4].(int64)),
		UpdatedAt:    time.UnixMilli(args[5].(int64)),
	}
	if _, ok := e.byAccount[profile.AccountName]; ok {
		return nil, &mysql.MySQLError{Number: 1062, Message: "duplicate"}
	}
	e.byAccount[profile.AccountName] = profile
	e.byID[profile.PlayerID] = profile
	return driver.RowsAffected(1), nil
}

func (e *memorySQLExecutor) QueryRowContext(ctx context.Context, query string, args ...any) SQLRow {
	if err := ctx.Err(); err != nil {
		return memorySQLRow{err: err}
	}
	key := args[0].(string)
	if strings.Contains(query, "WHERE account_name") {
		profile, ok := e.byAccount[key]
		if !ok {
			return memorySQLRow{err: sql.ErrNoRows}
		}
		return memorySQLRow{profile: profile}
	}
	profile, ok := e.byID[key]
	if !ok {
		return memorySQLRow{err: sql.ErrNoRows}
	}
	return memorySQLRow{profile: profile}
}

type memorySQLRow struct {
	profile PlayerProfile
	err     error
}

func (r memorySQLRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*string)) = r.profile.PlayerID
	*(dest[1].(*string)) = r.profile.AccountName
	*(dest[2].(*string)) = r.profile.PasswordHash
	*(dest[3].(*string)) = r.profile.DisplayName
	*(dest[4].(*int64)) = r.profile.CreatedAt.UnixMilli()
	*(dest[5].(*int64)) = r.profile.UpdatedAt.UnixMilli()
	return nil
}

type memoryRedisClient struct {
	values map[string][]byte
	ttls   map[string]time.Duration
}

func newMemoryRedisClient() *memoryRedisClient {
	return &memoryRedisClient{
		values: make(map[string][]byte),
		ttls:   make(map[string]time.Duration),
	}
}

func (c *memoryRedisClient) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.values[key] = append([]byte(nil), value...)
	c.ttls[key] = ttl
	return nil
}

func (c *memoryRedisClient) Del(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(c.values, key)
	delete(c.ttls, key)
	return nil
}

func (c *memoryRedisClient) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, ok := c.values[key]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), value...), nil
}
