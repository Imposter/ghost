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

	"github.com/Imposter/ghost/ghost-server/server/policy"
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

// ---- migrations ----

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
		err = s.inTx(ctx, func(tx *sql.Tx) error {
			for _, stmt := range splitStatements(string(body)) {
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("store: migration %s: %w", m.name, err)
				}
			}
			_, err := tx.ExecContext(ctx, s.q(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`),
				m.version, time.Now().UnixMilli())
			return err
		})
		if err != nil {
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

// ---- helpers ----

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

// forUpdate locks a selected row inside a transaction on PostgreSQL (SQLite
// serialises writers already).
func (s *SQL) forUpdate() string {
	if s.dialect == Postgres {
		return " FOR UPDATE"
	}
	return ""
}

func (s *SQL) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *SQL) execOne(ctx context.Context, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, s.q(query), args...)
	if err != nil {
		return mapWriteErr(err)
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

func mapWriteErr(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == "23505" || pgErr.Code == "23503" {
			return fmt.Errorf("%w: %s", ErrConflict, pgErr.Message)
		}
		return err
	}
	if msg := err.Error(); strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "FOREIGN KEY constraint failed") {
		return fmt.Errorf("%w: %s", ErrConflict, msg)
	}
	return err
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

type scanner interface{ Scan(...any) error }

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

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// jsonList marshals a slice, writing nil as [].
func jsonList[T any](v []T) string {
	if v == nil {
		return "[]"
	}
	return mustJSON(v)
}

// jsonMap marshals a map, writing nil as {}.
func jsonMap[V any](v map[string]V) string {
	if v == nil {
		return "{}"
	}
	return mustJSON(v)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("store: marshal %T: %v", v, err))
	}
	return string(b)
}

func fromJSON[T any](s string, dst *T) error {
	if s == "" {
		return nil
	}
	return json.Unmarshal([]byte(s), dst)
}

// ---- networks ----

const networkCols = `name, pool, isolation, interactive_enrollment, policy, policy_revision, created_at`

func scanNetwork(sc scanner) (Network, error) {
	var (
		n                    Network
		iso                  string
		interactive, created int64
		doc                  string
	)
	if err := sc.Scan(&n.Name, &n.Pool, &iso, &interactive, &doc, &n.PolicyRevision, &created); err != nil {
		return n, err
	}
	if err := fromJSON(doc, &n.Policy); err != nil {
		return n, fmt.Errorf("store: network %s policy: %w", n.Name, err)
	}
	n.Isolation = policy.Isolation(iso)
	n.InteractiveEnrollment = interactive != 0
	n.Policy = n.Policy.Normalize()
	n.CreatedAt = fromMs(created)
	return n, nil
}

func (s *SQL) CreateNetwork(ctx context.Context, n Network) error {
	if n.Isolation == "" {
		n.Isolation = policy.IsolationNone
	}
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO networks (`+networkCols+`) VALUES (?, ?, ?, ?, ?, ?, ?)`),
		n.Name, n.Pool, string(n.Isolation), b2i(n.InteractiveEnrollment), mustJSON(n.Policy.Normalize()),
		n.PolicyRevision, ms(n.CreatedAt))
	return mapWriteErr(err)
}

func (s *SQL) GetNetwork(ctx context.Context, name string) (Network, error) {
	n, err := scanNetwork(s.db.QueryRowContext(ctx, s.q(`SELECT `+networkCols+` FROM networks WHERE name = ?`), name))
	return n, notFound(err)
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

func (s *SQL) DeleteNetwork(ctx context.Context, name string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var peers int
		if err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM peers WHERE network = ?`), name).Scan(&peers); err != nil {
			return err
		}
		if peers > 0 {
			return fmt.Errorf("%w: network %s still has %d peers", ErrConflict, name, peers)
		}
		for _, q := range []string{`DELETE FROM auth_keys WHERE network = ?`, `DELETE FROM enrollments WHERE network = ?`} {
			if _, err := tx.ExecContext(ctx, s.q(q), name); err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, s.q(`DELETE FROM networks WHERE name = ?`), name)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// updateNetwork reads a network under lock, applies fn, bumps the policy
// revision if fn reports a policy change, and writes the settings back.
func (s *SQL) updateNetwork(ctx context.Context, name string, fn func(*Network) (policyChanged bool)) (Network, error) {
	var out Network
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		n, err := scanNetwork(tx.QueryRowContext(ctx, s.q(`SELECT `+networkCols+` FROM networks WHERE name = ?`+s.forUpdate()), name))
		if err != nil {
			return notFound(err)
		}
		if fn(&n) {
			n.PolicyRevision++
		}
		if _, err := tx.ExecContext(ctx, s.q(`UPDATE networks SET isolation = ?, interactive_enrollment = ?, policy = ?,
			policy_revision = ? WHERE name = ?`),
			string(n.Isolation), b2i(n.InteractiveEnrollment), mustJSON(n.Policy.Normalize()), n.PolicyRevision, name); err != nil {
			return err
		}
		out = n
		return nil
	})
	return out, err
}

func (s *SQL) SetNetworkPolicy(ctx context.Context, name string, doc policy.Document) (Network, error) {
	return s.updateNetwork(ctx, name, func(n *Network) bool {
		n.Policy = doc.Normalize()
		return true
	})
}

func (s *SQL) UpdateNetwork(ctx context.Context, name string, u NetworkUpdate) (Network, error) {
	return s.updateNetwork(ctx, name, func(n *Network) bool {
		if u.InteractiveEnrollment != nil {
			n.InteractiveEnrollment = *u.InteractiveEnrollment
		}
		if u.Isolation != nil {
			n.Isolation = *u.Isolation
			return true
		}
		return false
	})
}

// ---- peers ----

const peerCols = `id, token_hash, network, name, public_key, address, roles, tags, labels, endpoints, ephemeral, auth_key_id, enrollment_method, health, health_at, created_at, last_seen, expires_at, revoked_at`

func scanPeer(sc scanner) (Peer, error) {
	var (
		p                                      Peer
		address                                sql.NullString
		roles, tags, labels, endpoints, health string
		ephemeral, created                     int64
		healthAt, lastSeen, expiresAt, revoked sql.NullInt64
	)
	if err := sc.Scan(&p.ID, &p.TokenHash, &p.Network, &p.Name, &p.PublicKey, &address, &roles, &tags, &labels,
		&endpoints, &ephemeral, &p.AuthKeyID, &p.EnrollmentMethod, &health, &healthAt, &created, &lastSeen, &expiresAt, &revoked); err != nil {
		return p, err
	}
	if err := errors.Join(fromJSON(roles, &p.Roles), fromJSON(tags, &p.Tags), fromJSON(labels, &p.Labels),
		fromJSON(endpoints, &p.Endpoints)); err != nil {
		return p, fmt.Errorf("store: peer %s: %w", p.ID, err)
	}
	if health != "" {
		var h proto.Health
		if err := fromJSON(health, &h); err != nil {
			return p, fmt.Errorf("store: peer %s health: %w", p.ID, err)
		}
		p.Health = &h
	}
	if p.Labels == nil {
		p.Labels = map[string]string{}
	}
	p.Address = address.String
	p.Ephemeral = ephemeral != 0
	p.HealthAt = fromNullMs(healthAt)
	p.CreatedAt = fromMs(created)
	p.LastSeen = fromNullMs(lastSeen)
	p.ExpiresAt = fromNullMs(expiresAt)
	p.RevokedAt = fromNullMs(revoked)
	return p, nil
}

func peerArgs(p Peer) []any {
	health := ""
	if p.Health != nil {
		health = mustJSON(p.Health)
	}
	return []any{p.ID, p.TokenHash, p.Network, p.Name, p.PublicKey, nullString(p.Address),
		jsonList(p.Roles), jsonList(p.Tags), jsonMap(p.Labels), jsonList(p.Endpoints), b2i(p.Ephemeral),
		p.AuthKeyID, p.EnrollmentMethod, health, msPtr(p.HealthAt), ms(p.CreatedAt), msPtr(p.LastSeen), msPtr(p.ExpiresAt), msPtr(p.RevokedAt)}
}

func (s *SQL) CreatePeer(ctx context.Context, p Peer) error {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO peers (`+peerCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		peerArgs(p)...)
	return mapWriteErr(err)
}

func (s *SQL) GetPeer(ctx context.Context, id string) (Peer, error) {
	p, err := scanPeer(s.db.QueryRowContext(ctx, s.q(`SELECT `+peerCols+` FROM peers WHERE id = ?`), id))
	return p, notFound(err)
}

func (s *SQL) GetPeerByTokenHash(ctx context.Context, hash string) (Peer, error) {
	p, err := scanPeer(s.db.QueryRowContext(ctx, s.q(`SELECT `+peerCols+` FROM peers WHERE token_hash = ?`), hash))
	return p, notFound(err)
}

func (s *SQL) ListPeers(ctx context.Context, f PeerFilter) ([]Peer, error) {
	query := `SELECT ` + peerCols + ` FROM peers WHERE 1 = 1`
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
	var out []Peer
	for rows.Next() {
		p, err := scanPeer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *SQL) UpdatePeer(ctx context.Context, id string, fn func(*Peer) error) (Peer, error) {
	var out Peer
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		p, err := scanPeer(tx.QueryRowContext(ctx, s.q(`SELECT `+peerCols+` FROM peers WHERE id = ?`+s.forUpdate()), id))
		if err != nil {
			return notFound(err)
		}
		if err := fn(&p); err != nil {
			return err
		}
		p.ID = id
		args := peerArgs(p)
		_, err = tx.ExecContext(ctx, s.q(`UPDATE peers SET token_hash = ?, network = ?, name = ?, public_key = ?, address = ?,
			roles = ?, tags = ?, labels = ?, endpoints = ?, ephemeral = ?, auth_key_id = ?, enrollment_method = ?, health = ?,
			health_at = ?,
			created_at = ?, last_seen = ?, expires_at = ?, revoked_at = ? WHERE id = ?`), append(args[1:], id)...)
		if err != nil {
			return mapWriteErr(err)
		}
		out = p
		return nil
	})
	return out, err
}

func (s *SQL) DeletePeer(ctx context.Context, id string) error {
	return s.execOne(ctx, `DELETE FROM peers WHERE id = ?`, id)
}

func (s *SQL) UsedAddresses(ctx context.Context, network string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT address FROM peers WHERE network = ? AND address IS NOT NULL`), network)
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

func (s *SQL) TouchPeer(ctx context.Context, id string, at time.Time) error {
	return s.execOne(ctx, `UPDATE peers SET last_seen = ? WHERE id = ?`, ms(at), id)
}

func (s *SQL) SetPeerHealth(ctx context.Context, id string, h proto.Health, at time.Time) error {
	return s.execOne(ctx, `UPDATE peers SET health = ?, health_at = ?, last_seen = ? WHERE id = ?`, mustJSON(h), ms(at), ms(at), id)
}

// ---- pre-auth keys ----

const authKeyCols = `id, key_hash, network, reusable, ephemeral, roles, tags, labels, peer_ttl_ms, uses, created_at, expires_at, last_used_at, revoked_at`

func scanAuthKey(sc scanner) (AuthKey, error) {
	var (
		k                                      AuthKey
		reusable, ephemeral, ttl, created, exp int64
		roles, tags, labels                    string
		lastUsed, revoked                      sql.NullInt64
	)
	if err := sc.Scan(&k.ID, &k.KeyHash, &k.Network, &reusable, &ephemeral, &roles, &tags, &labels, &ttl, &k.Uses,
		&created, &exp, &lastUsed, &revoked); err != nil {
		return k, err
	}
	if err := errors.Join(fromJSON(roles, &k.Roles), fromJSON(tags, &k.Tags), fromJSON(labels, &k.Labels)); err != nil {
		return k, fmt.Errorf("store: auth key %s: %w", k.ID, err)
	}
	k.Reusable, k.Ephemeral = reusable != 0, ephemeral != 0
	k.PeerTTL = time.Duration(ttl) * time.Millisecond
	k.CreatedAt, k.ExpiresAt = fromMs(created), fromMs(exp)
	k.LastUsedAt, k.RevokedAt = fromNullMs(lastUsed), fromNullMs(revoked)
	return k, nil
}

func (s *SQL) CreateAuthKey(ctx context.Context, k AuthKey) error {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO auth_keys (`+authKeyCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		k.ID, k.KeyHash, k.Network, b2i(k.Reusable), b2i(k.Ephemeral), jsonList(k.Roles), jsonList(k.Tags), jsonMap(k.Labels),
		k.PeerTTL.Milliseconds(), k.Uses, ms(k.CreatedAt), ms(k.ExpiresAt), msPtr(k.LastUsedAt), msPtr(k.RevokedAt))
	return mapWriteErr(err)
}

func (s *SQL) GetAuthKeyByHash(ctx context.Context, hash string) (AuthKey, error) {
	k, err := scanAuthKey(s.db.QueryRowContext(ctx, s.q(`SELECT `+authKeyCols+` FROM auth_keys WHERE key_hash = ?`), hash))
	return k, notFound(err)
}

func (s *SQL) ListAuthKeys(ctx context.Context, network string) ([]AuthKey, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT `+authKeyCols+` FROM auth_keys WHERE network = ? ORDER BY created_at, id`), network)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuthKey
	for rows.Next() {
		k, err := scanAuthKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *SQL) UseAuthKey(ctx context.Context, id string, now time.Time) (AuthKey, error) {
	var out AuthKey
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, s.q(`UPDATE auth_keys SET uses = uses + 1, last_used_at = ?
			WHERE id = ? AND revoked_at IS NULL AND expires_at > ? AND (reusable = 1 OR uses = 0)`), ms(now), id, ms(now))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		out, err = scanAuthKey(tx.QueryRowContext(ctx, s.q(`SELECT `+authKeyCols+` FROM auth_keys WHERE id = ?`), id))
		return err
	})
	return out, err
}

func (s *SQL) RevokeAuthKey(ctx context.Context, id string, at time.Time) (AuthKey, error) {
	if _, err := s.db.ExecContext(ctx, s.q(`UPDATE auth_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`), ms(at), id); err != nil {
		return AuthKey{}, err
	}
	k, err := scanAuthKey(s.db.QueryRowContext(ctx, s.q(`SELECT `+authKeyCols+` FROM auth_keys WHERE id = ?`), id))
	return k, notFound(err)
}

// ---- enrolments ----

const enrollmentCols = `code_hash, poll_hash, network, name, public_key, labels, status, roles, tags, reason, peer_id, created_at, expires_at, decided_at`

func scanEnrollment(sc scanner) (Enrollment, error) {
	var (
		e                           Enrollment
		labels, status, roles, tags string
		created, exp                int64
		decided                     sql.NullInt64
	)
	if err := sc.Scan(&e.CodeHash, &e.PollHash, &e.Network, &e.Name, &e.PublicKey, &labels, &status, &roles, &tags,
		&e.Reason, &e.PeerID, &created, &exp, &decided); err != nil {
		return e, err
	}
	if err := errors.Join(fromJSON(labels, &e.Labels), fromJSON(roles, &e.Roles), fromJSON(tags, &e.Tags)); err != nil {
		return e, fmt.Errorf("store: enrollment: %w", err)
	}
	e.Status = EnrollmentStatus(status)
	e.CreatedAt, e.ExpiresAt, e.DecidedAt = fromMs(created), fromMs(exp), fromNullMs(decided)
	return e, nil
}

func enrollmentArgs(e Enrollment) []any {
	return []any{e.CodeHash, e.PollHash, e.Network, e.Name, e.PublicKey, jsonMap(e.Labels), string(e.Status),
		jsonList(e.Roles), jsonList(e.Tags), e.Reason, e.PeerID, ms(e.CreatedAt), ms(e.ExpiresAt), msPtr(e.DecidedAt)}
}

func (s *SQL) CreateEnrollment(ctx context.Context, e Enrollment) error {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO enrollments (`+enrollmentCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		enrollmentArgs(e)...)
	return mapWriteErr(err)
}

func (s *SQL) GetEnrollmentByCode(ctx context.Context, codeHash string) (Enrollment, error) {
	e, err := scanEnrollment(s.db.QueryRowContext(ctx, s.q(`SELECT `+enrollmentCols+` FROM enrollments WHERE code_hash = ?`), codeHash))
	return e, notFound(err)
}

func (s *SQL) GetEnrollmentByPoll(ctx context.Context, pollHash string) (Enrollment, error) {
	e, err := scanEnrollment(s.db.QueryRowContext(ctx, s.q(`SELECT `+enrollmentCols+` FROM enrollments WHERE poll_hash = ?`), pollHash))
	return e, notFound(err)
}

func (s *SQL) ListEnrollments(ctx context.Context, network string, status EnrollmentStatus) ([]Enrollment, error) {
	query := `SELECT ` + enrollmentCols + ` FROM enrollments WHERE network = ?`
	args := []any{network}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, string(status))
	}
	rows, err := s.db.QueryContext(ctx, s.q(query+` ORDER BY created_at`), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Enrollment
	for rows.Next() {
		e, err := scanEnrollment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *SQL) UpdateEnrollment(ctx context.Context, codeHash string, fn func(*Enrollment) error) (Enrollment, error) {
	var out Enrollment
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		e, err := scanEnrollment(tx.QueryRowContext(ctx, s.q(`SELECT `+enrollmentCols+` FROM enrollments WHERE code_hash = ?`+s.forUpdate()), codeHash))
		if err != nil {
			return notFound(err)
		}
		if err := fn(&e); err != nil {
			return err
		}
		args := enrollmentArgs(e)
		_, err = tx.ExecContext(ctx, s.q(`UPDATE enrollments SET poll_hash = ?, network = ?, name = ?, public_key = ?, labels = ?,
			status = ?, roles = ?, tags = ?, reason = ?, peer_id = ?, created_at = ?, expires_at = ?, decided_at = ?
			WHERE code_hash = ?`), append(args[1:], codeHash)...)
		out = e
		return mapWriteErr(err)
	})
	return out, err
}

func (s *SQL) DeleteEnrollmentsBefore(ctx context.Context, t time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, s.q(`DELETE FROM enrollments WHERE expires_at < ?`), ms(t))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---- API keys ----

const apiKeyCols = `id, key_hash, name, scopes, networks, created_at, expires_at, last_used_at, revoked_at`

func scanAPIKey(sc scanner) (APIKey, error) {
	var (
		k                          APIKey
		scopes, networks           string
		created                    int64
		expires, lastUsed, revoked sql.NullInt64
	)
	if err := sc.Scan(&k.ID, &k.KeyHash, &k.Name, &scopes, &networks, &created, &expires, &lastUsed, &revoked); err != nil {
		return k, err
	}
	if err := errors.Join(fromJSON(scopes, &k.Scopes), fromJSON(networks, &k.Networks)); err != nil {
		return k, fmt.Errorf("store: api key %s: %w", k.ID, err)
	}
	k.CreatedAt = fromMs(created)
	k.ExpiresAt, k.LastUsedAt, k.RevokedAt = fromNullMs(expires), fromNullMs(lastUsed), fromNullMs(revoked)
	return k, nil
}

func (s *SQL) CreateAPIKey(ctx context.Context, k APIKey) error {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO api_keys (`+apiKeyCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		k.ID, k.KeyHash, k.Name, jsonList(k.Scopes), jsonList(k.Networks), ms(k.CreatedAt), msPtr(k.ExpiresAt),
		msPtr(k.LastUsedAt), msPtr(k.RevokedAt))
	return mapWriteErr(err)
}

func (s *SQL) GetAPIKeyByHash(ctx context.Context, hash string) (APIKey, error) {
	k, err := scanAPIKey(s.db.QueryRowContext(ctx, s.q(`SELECT `+apiKeyCols+` FROM api_keys WHERE key_hash = ?`), hash))
	return k, notFound(err)
}

func (s *SQL) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+apiKeyCols+` FROM api_keys ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *SQL) RevokeAPIKey(ctx context.Context, id string, at time.Time) (APIKey, error) {
	if _, err := s.db.ExecContext(ctx, s.q(`UPDATE api_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`), ms(at), id); err != nil {
		return APIKey{}, err
	}
	k, err := scanAPIKey(s.db.QueryRowContext(ctx, s.q(`SELECT `+apiKeyCols+` FROM api_keys WHERE id = ?`), id))
	return k, notFound(err)
}

func (s *SQL) TouchAPIKey(ctx context.Context, id string, at time.Time) error {
	return s.execOne(ctx, `UPDATE api_keys SET last_used_at = ? WHERE id = ?`, ms(at), id)
}

// ---- audit ----

func (s *SQL) AppendAudit(ctx context.Context, e AuditEvent) error {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO audit_events (id, ts, network, actor, action, target, detail) VALUES (?, ?, ?, ?, ?, ?, ?)`),
		e.ID, ms(e.Time), e.Network, e.Actor, e.Action, e.Target, jsonMap(e.Detail))
	return mapWriteErr(err)
}

func (s *SQL) ListAudit(ctx context.Context, f AuditFilter) ([]AuditEvent, error) {
	query := `SELECT id, ts, network, actor, action, target, detail FROM audit_events WHERE ts >= ?`
	args := []any{ms(f.Since)}
	if f.Network != "" {
		query += ` AND network = ?`
		args = append(args, f.Network)
	}
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	query += ` ORDER BY ts, id LIMIT ` + strconv.Itoa(limit)
	rows, err := s.db.QueryContext(ctx, s.q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		var (
			e      AuditEvent
			ts     int64
			detail string
		)
		if err := rows.Scan(&e.ID, &ts, &e.Network, &e.Actor, &e.Action, &e.Target, &detail); err != nil {
			return nil, err
		}
		e.Time = fromMs(ts)
		if err := fromJSON(detail, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- stats ----

func (s *SQL) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM networks),
		(SELECT COUNT(*) FROM peers),
		(SELECT COUNT(*) FROM peers WHERE revoked_at IS NOT NULL)`).
		Scan(&st.Networks, &st.Peers, &st.RevokedPeers)
	return st, err
}
