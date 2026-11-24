package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/sourcestatus"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// SourceStatusStore adalah ports.SourceStatusStore di atas pool pgx
// (tabel ts.source_status, ADR 0030).
type SourceStatusStore struct {
	pool *pgxpool.Pool
}

var _ ports.SourceStatusStore = (*SourceStatusStore)(nil)

// NewSourceStatusStore membungkus pool yang sudah terbuka. Pemanggil yang menutup pool.
func NewSourceStatusStore(pool *pgxpool.Pool) *SourceStatusStore {
	return &SourceStatusStore{pool: pool}
}

const (
	selectSourceStatus = `
SELECT subject, poll_interval, last_attempt_at, last_success_at, last_change_at, failing_since, consecutive_failures
FROM ts.source_status WHERE connector = $1 FOR UPDATE`

	upsertSourceStatus = `
INSERT INTO ts.source_status AS s
  (connector, subject, poll_interval, last_attempt_at, last_success_at, last_change_at, failing_since, consecutive_failures)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (connector) DO UPDATE SET
  subject = EXCLUDED.subject, poll_interval = EXCLUDED.poll_interval,
  last_attempt_at = EXCLUDED.last_attempt_at, last_success_at = EXCLUDED.last_success_at,
  last_change_at = EXCLUDED.last_change_at, failing_since = EXCLUDED.failing_since,
  consecutive_failures = EXCLUDED.consecutive_failures, updated_at = now()`
)

// Update membaca baris konektor dengan FOR UPDATE, memanggil fn, lalu
// menyimpan hasilnya dalam transaksi yang sama. Dua konektor baru yang
// tercatat bersamaan bisa bentrok di primary key; galatnya sementara dan
// pesan yang kalah dicoba ulang consumer.
func (s *SourceStatusStore) Update(ctx context.Context, connector string, fn func(*sourcestatus.State) (sourcestatus.State, bool)) (saved bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("memulai transaksi: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, rollback(ctx, tx))
		}
	}()
	prev, err := loadSourceStatus(ctx, tx, connector)
	if err != nil {
		return false, err
	}
	next, save := fn(prev)
	if !save {
		return false, tx.Rollback(ctx)
	}
	if _, err = tx.Exec(ctx, upsertSourceStatus,
		next.Connector, next.Subject, pgtype.Interval{Microseconds: next.Interval.Microseconds(), Valid: true},
		next.LastAttemptAt, optionalTime(next.LastSuccessAt), optionalTime(next.LastChangeAt), optionalTime(next.FailingSince),
		next.ConsecutiveFailures,
	); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23514" {
			return false, fmt.Errorf("%w: status %s melanggar constraint %s: %w", sourcestatus.ErrInvalid, connector, pgErr.ConstraintName, err)
		}
		return false, fmt.Errorf("menyimpan status %s: %w", connector, err)
	}
	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return true, nil
}

func loadSourceStatus(ctx context.Context, tx pgx.Tx, connector string) (*sourcestatus.State, error) {
	st := sourcestatus.State{Connector: connector}
	var interval pgtype.Interval
	var success, change, failing pgtype.Timestamptz
	err := tx.QueryRow(ctx, selectSourceStatus, connector).Scan(
		&st.Subject, &interval, &st.LastAttemptAt, &success, &change, &failing, &st.ConsecutiveFailures)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("membaca status %s: %w", connector, err)
	}
	if interval.Months != 0 || interval.Days != 0 {
		return nil, fmt.Errorf("%w: interval %s berisi hari/bulan", sourcestatus.ErrInvalid, connector)
	}
	st.Interval = time.Duration(interval.Microseconds) * time.Microsecond
	st.LastAttemptAt = st.LastAttemptAt.UTC()
	st.LastSuccessAt, st.LastChangeAt, st.FailingSince = timeOrZero(success), timeOrZero(change), timeOrZero(failing)
	return &st, nil
}

func optionalTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()}
}

func timeOrZero(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time.UTC()
}
