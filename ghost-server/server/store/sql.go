package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
	_ "modernc.org/sqlite"             // registers the "sqlite" driver

	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Dialect identifies the SQL backend.
type Dialect string

const (
	SQLite   Dialect = "sqlite"
	Postgres Dialect = "postgres"
)

// SQL is a Store over database/sql.
type SQL struct {
	db      *sql.DB
	dialect Dialect
}

var _ Store = (*SQL)(nil)

// Open connects to the database, applies pending migrations, and returns a
// Store. driver is "sqlite" (dsn is a file path) or "postgres" (dsn is a
// postgres:// URL).
func Open(ctx context.Context, driver, dsn string) (*SQL, error) {
	var (
		db  *sql.DB
		err error
	)
	switch Dialect(driver) {
	case SQLite:
		db, err = sql.Open("sqlite", sqliteDSN(dsn))
		if err == nil {
			// SQLite serialises writers; one connection avoids SQLITE_BUSY.
			db.SetMaxOpenConns(1)
		}
	case Postgres:
		db, err = sql.Open("pgx", dsn)
	default:
		return nil, fmt.Errorf("store: unknown driver %q", driver)
	}
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	s := &SQL{db: db, dialect: Dialect(driver)}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func sqliteDSN(path string) string {
	if strings.HasPrefix(path, "file:") || strings.Contains(path, "?") {
		return path
	}
	return "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
}

// Close closes the database.
func (s *SQL) Close() error { return s.db.Close() }

// migrate applies embedded migrations in version order, each in its own
// transaction, recording them in schema_migrations.
func (s *SQL) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version BIGINT PRIMARY KEY, applied_at BIGINT NOT NULL)`); err != nil {
		return fmt.Errorf("store: migrations table: %w", err)
	}
	applied := map[int64]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("store: read migrations: %w", err)
	}
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()

	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return err
	}
	type migration struct {
		version int64
		name    string
	}
	var pending []migration
	for _, e := range entries {
		prefix, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return fmt.Errorf("store: migration %q lacks a version prefix", e.Name())
		}
		v, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			return fmt.Errorf("store: migration %q: %w", e.Name(), err)
		}
		pending = append(pending, migration{v, e.Name()})
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].version < pending[j].version })

	for _, m := range pending {
		if applied[m.version] {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + m.name)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, stmt := range splitStatements(string(body)) {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("store: migration %s: %w", m.name, err)
			}
		}
		if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`),
			m.version, time.Now().UnixMilli()); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// splitStatements splits a migration on semicolons that end a line, dropping
// comment-only lines. Migrations must not contain semicolons inside literals.
func splitStatements(body string) []string {
	var (
		out []string
		cur strings.Builder
	)
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		cur.WriteString(line)
		cur.WriteString("\n")
		if strings.HasSuffix(trimmed, ";") {
			out = append(out, strings.TrimSuffix(strings.TrimSpace(cur.String()), ";"))
			cur.Reset()
		}
	}
	if rest := strings.TrimSpace(cur.String()); rest != "" {
		out = append(out, rest)
	}
	return out
}

// q rewrites "?" placeholders to "$n" for PostgreSQL.
func (s *SQL) q(query string) string {
	if s.dialect != Postgres {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func msPtr(t *time.Time) sql.NullInt64 {
	if t == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UnixMilli(), Valid: true}
}

func fromMs(v int64) time.Time { return time.UnixMilli(v).UTC() }

func fromNullMs(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := fromMs(v.Int64)
	return &t
}

func encodeJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func labelsJSON(l map[string]string) (string, error) {
	if l == nil {
		l = map[string]string{}
	}
	return encodeJSON(l)
}

func decodeLabels(s string) (map[string]string, error) {
	out := map[string]string{}
	if s == "" {
		return out, nil
	}
	return out, json.Unmarshal([]byte(s), &out)
}

// ---- networks ----

func (s *SQL) CreateNetwork(ctx context.Context, n Network) error {
	pol := n.Policy
	pol.Network = n.Name
	pol.Revision = 0
	polJSON, err := encodeJSON(pol)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, s.q(`INSERT INTO networks (name, pool, policy, policy_revision, created_at) VALUES (?, ?, ?, 0, ?)`),
		n.Name, n.Pool, polJSON, ms(n.CreatedAt))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

const networkCols = `name, pool, policy, policy_revision, created_at`

func scanNetwork(sc interface{ Scan(...any) error }) (Network, error) {
	var (
		n       Network
		polJSON string
		rev     int64
		created int64
	)
	if err := sc.Scan(&n.Name, &n.Pool, &polJSON, &rev, &created); err != nil {
		return n, err
	}
	if err := json.Unmarshal([]byte(polJSON), &n.Policy); err != nil {
		return n, fmt.Errorf("store: network %s policy: %w", n.Name, err)
	}
	n.Policy.Network = n.Name
	n.Policy.Revision = rev
	if n.Policy.Allow == nil {
		n.Policy.Allow = []string{}
	}
	n.CreatedAt = fromMs(created)
	return n, nil
}

func (s *SQL) GetNetwork(ctx context.Context, name string) (Network, error) {
	row := s.db.QueryRowContext(ctx, s.q(`SELECT `+networkCols+` FROM networks WHERE name = ?`), name)
	n, err := scanNetwork(row)
	if errors.Is(err, sql.ErrNoRows) {
		return n, ErrNotFound
	}
	return n, err
}

func (s *SQL) ListNetworks(ctx context.Context) ([]Network, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+networkCols+` FROM networks ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Network
	for rows.Next() {
		n, err := scanNetwork(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *SQL) SetNetworkPolicy(ctx context.Context, name string, p proto.ExitPolicy) (proto.ExitPolicy, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer func() { _ = tx.Rollback() }()
	var rev int64
	err = tx.QueryRowContext(ctx, s.q(`SELECT policy_revision FROM networks WHERE name = ?`), name).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	p.Network = name
	p.Revision = rev + 1
	if p.Allow == nil {
		p.Allow = []string{}
	}
	polJSON, err := encodeJSON(p)
	if err != nil {
		return p, err
	}
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE networks SET policy = ?, policy_revision = ? WHERE name = ?`),
		polJSON, p.Revision, name); err != nil {
		return p, err
	}
	return p, tx.Commit()
}

// ---- devices ----

const deviceCols = `id, token_hash, network, role, name, labels, public_key, address, created_at, last_seen, revoked_at`

func scanDevice(sc interface{ Scan(...any) error }) (Device, error) {
	var (
		d         Device
		role      string
		labels    string
		address   sql.NullString
		created   int64
		lastSeen  sql.NullInt64
		revokedAt sql.NullInt64
	)
	if err := sc.Scan(&d.ID, &d.TokenHash, &d.Network, &role, &d.Name, &labels, &d.PublicKey,
		&address, &created, &lastSeen, &revokedAt); err != nil {
		return d, err
	}
	var err error
	if d.Labels, err = decodeLabels(labels); err != nil {
		return d, fmt.Errorf("store: device %s labels: %w", d.ID, err)
	}
	d.Role = proto.Role(role)
	d.Address = address.String
	d.CreatedAt = fromMs(created)
	d.LastSeen = fromNullMs(lastSeen)
	d.RevokedAt = fromNullMs(revokedAt)
	return d, nil
}

func (s *SQL) CreateDevice(ctx context.Context, d Device) error {
	labels, err := labelsJSON(d.Labels)
	if err != nil {
		return err
	}
	var address sql.NullString
	if d.Address != "" {
		address = sql.NullString{String: d.Address, Valid: true}
	}
	_, err = s.db.ExecContext(ctx, s.q(`INSERT INTO devices (`+deviceCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		d.ID, d.TokenHash, d.Network, string(d.Role), d.Name, labels, d.PublicKey, address,
		ms(d.CreatedAt), msPtr(d.LastSeen), msPtr(d.RevokedAt))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

func (s *SQL) getDevice(ctx context.Context, where string, arg any) (Device, error) {
	row := s.db.QueryRowContext(ctx, s.q(`SELECT `+deviceCols+` FROM devices WHERE `+where+` = ?`), arg)
	d, err := scanDevice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

func (s *SQL) GetDevice(ctx context.Context, id string) (Device, error) {
	return s.getDevice(ctx, "id", id)
}

func (s *SQL) GetDeviceByTokenHash(ctx context.Context, hash string) (Device, error) {
	return s.getDevice(ctx, "token_hash", hash)
}

func (s *SQL) ListDevices(ctx context.Context, f DeviceFilter) ([]Device, error) {
	query := `SELECT ` + deviceCols + ` FROM devices WHERE 1 = 1`
	var args []any
	if f.Network != "" {
		query += ` AND network = ?`
		args = append(args, f.Network)
	}
	if !f.IncludeRevoked {
		query += ` AND revoked_at IS NULL`
	}
	query += ` ORDER BY created_at, id`
	rows, err := s.db.QueryContext(ctx, s.q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *SQL) UsedAddresses(ctx context.Context, network string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT address FROM devices WHERE network = ? AND address IS NOT NULL`), network)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *SQL) execOne(ctx context.Context, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, s.q(query), args...)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQL) SetDeviceAddress(ctx context.Context, id, address string) error {
	return s.execOne(ctx, `UPDATE devices SET address = ? WHERE id = ?`, address, id)
}

func (s *SQL) SetDevicePublicKey(ctx context.Context, id, key string) error {
	return s.execOne(ctx, `UPDATE devices SET public_key = ? WHERE id = ?`, key, id)
}

func (s *SQL) TouchDevice(ctx context.Context, id string, at time.Time) error {
	return s.execOne(ctx, `UPDATE devices SET last_seen = ? WHERE id = ?`, ms(at), id)
}

func (s *SQL) RevokeDevice(ctx context.Context, id string, at time.Time) error {
	// Revoking twice keeps the first timestamp.
	if err := s.execOne(ctx, `UPDATE devices SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, ms(at), id); err != nil {
		if errors.Is(err, ErrNotFound) {
			if _, gerr := s.GetDevice(ctx, id); gerr == nil {
				return nil
			}
		}
		return err
	}
	return nil
}

func (s *SQL) MoveDevice(ctx context.Context, id, network string) error {
	return s.execOne(ctx, `UPDATE devices SET network = ?, address = NULL WHERE id = ?`, network, id)
}

// ---- pairing codes ----

func (s *SQL) CreatePairingCode(ctx context.Context, c PairingCode) error {
	labels, err := labelsJSON(c.Labels)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, s.q(`INSERT INTO pairing_codes (code_hash, network, role, name, labels, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`),
		c.CodeHash, c.Network, string(c.Role), c.Name, labels, ms(c.CreatedAt), ms(c.ExpiresAt))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

const pairingCols = `code_hash, network, role, name, labels, created_at, expires_at, used_at, device_id`

func scanPairingCode(sc interface{ Scan(...any) error }) (PairingCode, error) {
	var (
		c                PairingCode
		role, labels     string
		created, expires int64
		usedAt           sql.NullInt64
		deviceID         sql.NullString
	)
	if err := sc.Scan(&c.CodeHash, &c.Network, &role, &c.Name, &labels, &created, &expires, &usedAt, &deviceID); err != nil {
		return c, err
	}
	var err error
	if c.Labels, err = decodeLabels(labels); err != nil {
		return c, err
	}
	c.Role = proto.Role(role)
	c.CreatedAt = fromMs(created)
	c.ExpiresAt = fromMs(expires)
	c.UsedAt = fromNullMs(usedAt)
	c.DeviceID = deviceID.String
	return c, nil
}

func (s *SQL) GetPairingCode(ctx context.Context, codeHash string) (PairingCode, error) {
	c, err := scanPairingCode(s.db.QueryRowContext(ctx, s.q(`SELECT `+pairingCols+` FROM pairing_codes WHERE code_hash = ?`), codeHash))
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

func (s *SQL) RedeemPairingCode(ctx context.Context, codeHash string, now time.Time) (PairingCode, error) {
	var c PairingCode
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return c, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, s.q(`UPDATE pairing_codes SET used_at = ? WHERE code_hash = ? AND used_at IS NULL AND expires_at > ?`),
		ms(now), codeHash, ms(now))
	if err != nil {
		return c, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return c, err
	} else if n == 0 {
		return c, ErrNotFound
	}
	c, err = scanPairingCode(tx.QueryRowContext(ctx, s.q(`SELECT `+pairingCols+` FROM pairing_codes WHERE code_hash = ?`), codeHash))
	if err != nil {
		return c, err
	}
	return c, tx.Commit()
}

func (s *SQL) BindPairingCode(ctx context.Context, codeHash, deviceID string) error {
	return s.execOne(ctx, `UPDATE pairing_codes SET device_id = ? WHERE code_hash = ?`, deviceID, codeHash)
}

func (s *SQL) DeleteExpiredPairingCodes(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, s.q(`DELETE FROM pairing_codes WHERE expires_at <= ?`), ms(now))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---- stats ----

func (s *SQL) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM networks),
		(SELECT COUNT(*) FROM devices),
		(SELECT COUNT(*) FROM devices WHERE revoked_at IS NOT NULL)`).
		Scan(&st.Networks, &st.Devices, &st.RevokedDevices)
	return st, err
}
