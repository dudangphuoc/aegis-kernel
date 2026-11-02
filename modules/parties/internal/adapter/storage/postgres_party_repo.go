package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/dudangphuoc/aegis-kernel/modules/parties/internal/domain"
	"github.com/dudangphuoc/aegis-kernel/sdk/tenantdb"
	_ "github.com/lib/pq"
)

// PostgresPartyRepo làm việc trên schema biz_parties của DB tenant: INSERT/UPDATE (kể cả upsert) trên parties
// được trigger capture ghi trước/sau cùng transaction, repository không tự ghi audit.
type PostgresPartyRepo struct {
	dbm *tenantdb.Manager
}

func NewPostgresPartyRepo(dbm *tenantdb.Manager) *PostgresPartyRepo {
	return &PostgresPartyRepo{dbm: dbm}
}

func (r *PostgresPartyRepo) Save(ctx context.Context, p *domain.Party) error {
	db, tenant, err := r.dbm.Resolve(ctx)
	if err != nil {
		return err
	}

	// JSONB truyền dạng string: với binary_parameters=yes (PgBouncer), []byte bị gửi ở định dạng binary
	// và Postgres từ chối jsonb thiếu byte version
	empJSON, _ := json.Marshal(p.Employee)
	custJSON, _ := json.Marshal(p.Customer)

	query := `
	INSERT INTO parties (
		id, tenant_id, party_type, full_name, email, phone,
		identity_type, identity_number, identity_issued_date, identity_issued_place,
		verification_status, employee_profile, customer_profile, processed_by, created_at
	) VALUES (
		$1, $2, $3, $4, $5, $6,
		$7, $8, $9, $10,
		$11, $12, $13, $14, $15
	)
	ON CONFLICT (store_id, identity_number) DO UPDATE SET
		full_name = EXCLUDED.full_name,
		email = EXCLUDED.email,
		phone = EXCLUDED.phone,
		verification_status = EXCLUDED.verification_status,
		employee_profile = EXCLUDED.employee_profile,
		customer_profile = EXCLUDED.customer_profile,
		processed_by = EXCLUDED.processed_by;`

	createdAt := p.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	err = db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, query,
			p.ID, tenant.TenantID, string(p.Type), p.FullName, p.Email, p.Phone,
			string(p.Identity.Type), p.Identity.Number, p.Identity.IssuedDate, p.Identity.IssuedPlace,
			p.Identity.VerificationStatus, string(empJSON), string(custJSON), p.ProcessedBy, createdAt,
		)
		return err
	})
	if err != nil && strings.Contains(err.Error(), "duplicate key value") {
		return domain.ErrDuplicateIdentity
	}
	return err
}

func (r *PostgresPartyRepo) FindByID(ctx context.Context, id string) (*domain.Party, error) {
	db, _, err := r.dbm.Resolve(ctx)
	if err != nil {
		return nil, err
	}

	query := `
	SELECT id, tenant_id, party_type, full_name, email, phone,
		   identity_type, identity_number, identity_issued_date, identity_issued_place,
		   verification_status, employee_profile, customer_profile, processed_by, created_at
	FROM parties WHERE id = $1`

	var p *domain.Party
	err = db.Read(ctx, func(tx *sql.Tx) error {
		p, err = r.scanParty(tx.QueryRowContext(ctx, query, id))
		return err
	})
	return p, err
}

func (r *PostgresPartyRepo) FindByIdentityNumber(ctx context.Context, idNumber string) (*domain.Party, error) {
	db, _, err := r.dbm.Resolve(ctx)
	if err != nil {
		return nil, err
	}

	query := `
	SELECT id, tenant_id, party_type, full_name, email, phone,
		   identity_type, identity_number, identity_issued_date, identity_issued_place,
		   verification_status, employee_profile, customer_profile, processed_by, created_at
	FROM parties WHERE identity_number = $1`

	var p *domain.Party
	err = db.Read(ctx, func(tx *sql.Tx) error {
		p, err = r.scanParty(tx.QueryRowContext(ctx, query, idNumber))
		return err
	})
	return p, err
}

func (r *PostgresPartyRepo) List(ctx context.Context, filterType string) ([]*domain.Party, error) {
	db, _, err := r.dbm.Resolve(ctx)
	if err != nil {
		return nil, err
	}

	var res []*domain.Party
	err = db.Read(ctx, func(tx *sql.Tx) error {
		res, err = r.list(ctx, tx, filterType)
		return err
	})
	return res, err
}

func (r *PostgresPartyRepo) list(ctx context.Context, tx *sql.Tx, filterType string) ([]*domain.Party, error) {
	var query string
	var rows *sql.Rows
	var err error
	if filterType != "" {
		query = `
		SELECT id, tenant_id, party_type, full_name, email, phone,
			   identity_type, identity_number, identity_issued_date, identity_issued_place,
			   verification_status, employee_profile, customer_profile, processed_by, created_at
		FROM parties WHERE UPPER(party_type) = UPPER($1) ORDER BY created_at DESC`
		rows, err = tx.QueryContext(ctx, query, filterType)
	} else {
		query = `
		SELECT id, tenant_id, party_type, full_name, email, phone,
			   identity_type, identity_number, identity_issued_date, identity_issued_place,
			   verification_status, employee_profile, customer_profile, processed_by, created_at
		FROM parties ORDER BY created_at DESC`
		rows, err = tx.QueryContext(ctx, query)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []*domain.Party
	for rows.Next() {
		p, err := r.scanRow(rows)
		if err != nil {
			return nil, err
		}
		res = append(res, p)
	}
	return res, rows.Err()
}

func (r *PostgresPartyRepo) scanParty(row *sql.Row) (*domain.Party, error) {
	var p domain.Party
	var partyType, idType string
	var empJSON, custJSON []byte

	err := row.Scan(
		&p.ID, &p.TenantID, &partyType, &p.FullName, &p.Email, &p.Phone,
		&idType, &p.Identity.Number, &p.Identity.IssuedDate, &p.Identity.IssuedPlace,
		&p.Identity.VerificationStatus, &empJSON, &custJSON, &p.ProcessedBy, &p.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrPartyNotFound
	}
	if err != nil {
		return nil, err
	}

	p.Type = domain.PartyType(partyType)
	p.Identity.Type = domain.IdentityType(idType)
	if len(empJSON) > 0 && string(empJSON) != "null" {
		_ = json.Unmarshal(empJSON, &p.Employee)
	}
	if len(custJSON) > 0 && string(custJSON) != "null" {
		_ = json.Unmarshal(custJSON, &p.Customer)
	}
	return &p, nil
}

func (r *PostgresPartyRepo) scanRow(rows *sql.Rows) (*domain.Party, error) {
	var p domain.Party
	var partyType, idType string
	var empJSON, custJSON []byte

	err := rows.Scan(
		&p.ID, &p.TenantID, &partyType, &p.FullName, &p.Email, &p.Phone,
		&idType, &p.Identity.Number, &p.Identity.IssuedDate, &p.Identity.IssuedPlace,
		&p.Identity.VerificationStatus, &empJSON, &custJSON, &p.ProcessedBy, &p.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	p.Type = domain.PartyType(partyType)
	p.Identity.Type = domain.IdentityType(idType)
	if len(empJSON) > 0 && string(empJSON) != "null" {
		_ = json.Unmarshal(empJSON, &p.Employee)
	}
	if len(custJSON) > 0 && string(custJSON) != "null" {
		_ = json.Unmarshal(custJSON, &p.Customer)
	}
	return &p, nil
}
