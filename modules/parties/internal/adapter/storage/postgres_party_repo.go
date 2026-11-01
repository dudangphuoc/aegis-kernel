package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/lib/pq"
	partiesmigrations "github.com/dudangphuoc/AegisKernel/modules/parties/migrations"
	"github.com/dudangphuoc/AegisKernel/modules/parties/internal/domain"
	"github.com/dudangphuoc/AegisKernel/sdk/migrator"
	"github.com/dudangphuoc/AegisKernel/sdk/multitenant"
)

type PostgresPartyRepo struct {
	connManager *multitenant.ConnectionManager[*sql.DB]
}

func NewPostgresPartyRepo() *PostgresPartyRepo {
	runner := migrator.NewRunner()
	cm := multitenant.NewConnectionManager[*sql.DB](func(tenantID, connStr string) (*sql.DB, error) {
		db, err := sql.Open("postgres", connStr)
		if err != nil {
			return nil, fmt.Errorf("failed to open postgres connection for %s: %w", tenantID, err)
		}
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(2)
		db.SetConnMaxLifetime(time.Hour)

		// Tự động chạy migrations v1 khởi tạo
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := runner.MigrateUp(ctx, db, partiesmigrations.GetPartiesMigrationScripts()); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to auto-migrate parties db for %s: %w", tenantID, err)
		}

		return db, nil
	})
	return &PostgresPartyRepo{connManager: cm}
}

func (r *PostgresPartyRepo) Save(ctx context.Context, p *domain.Party) error {
	db, tenant, err := r.connManager.Resolve(ctx)
	if err != nil {
		return err
	}

	empJSON, _ := json.Marshal(p.Employee)
	custJSON, _ := json.Marshal(p.Customer)

	query := `
	INSERT INTO parties (
		id, tenant_id, party_type, full_name, email, phone,
		identity_type, identity_number, identity_issued_date, identity_issued_place,
		verification_status, employee_profile, customer_profile, processed_by, database_source, created_at
	) VALUES (
		$1, $2, $3, $4, $5, $6,
		$7, $8, $9, $10,
		$11, $12, $13, $14, $15, $16
	)
	ON CONFLICT (identity_number) DO UPDATE SET
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

	_, err = db.ExecContext(ctx, query,
		p.ID, tenant.TenantID, string(p.Type), p.FullName, p.Email, p.Phone,
		string(p.Identity.Type), p.Identity.Number, p.Identity.IssuedDate, p.Identity.IssuedPlace,
		p.Identity.VerificationStatus, empJSON, custJSON, p.ProcessedBy, tenant.ConnectionString, createdAt,
	)
	if err != nil && strings.Contains(err.Error(), "duplicate key value") {
		return domain.ErrDuplicateIdentity
	}
	return err
}

func (r *PostgresPartyRepo) FindByID(ctx context.Context, id string) (*domain.Party, error) {
	db, _, err := r.connManager.Resolve(ctx)
	if err != nil {
		return nil, err
	}

	query := `
	SELECT id, tenant_id, party_type, full_name, email, phone,
		   identity_type, identity_number, identity_issued_date, identity_issued_place,
		   verification_status, employee_profile, customer_profile, processed_by, database_source, created_at
	FROM parties WHERE id = $1`

	return r.scanParty(db.QueryRowContext(ctx, query, id))
}

func (r *PostgresPartyRepo) FindByIdentityNumber(ctx context.Context, idNumber string) (*domain.Party, error) {
	db, _, err := r.connManager.Resolve(ctx)
	if err != nil {
		return nil, err
	}

	query := `
	SELECT id, tenant_id, party_type, full_name, email, phone,
		   identity_type, identity_number, identity_issued_date, identity_issued_place,
		   verification_status, employee_profile, customer_profile, processed_by, database_source, created_at
	FROM parties WHERE identity_number = $1`

	return r.scanParty(db.QueryRowContext(ctx, query, idNumber))
}

func (r *PostgresPartyRepo) List(ctx context.Context, filterType string) ([]*domain.Party, error) {
	db, _, err := r.connManager.Resolve(ctx)
	if err != nil {
		return nil, err
	}

	var query string
	var rows *sql.Rows
	if filterType != "" {
		query = `
		SELECT id, tenant_id, party_type, full_name, email, phone,
			   identity_type, identity_number, identity_issued_date, identity_issued_place,
			   verification_status, employee_profile, customer_profile, processed_by, database_source, created_at
		FROM parties WHERE UPPER(party_type) = UPPER($1) ORDER BY created_at DESC`
		rows, err = db.QueryContext(ctx, query, filterType)
	} else {
		query = `
		SELECT id, tenant_id, party_type, full_name, email, phone,
			   identity_type, identity_number, identity_issued_date, identity_issued_place,
			   verification_status, employee_profile, customer_profile, processed_by, database_source, created_at
		FROM parties ORDER BY created_at DESC`
		rows, err = db.QueryContext(ctx, query)
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
		&p.Identity.VerificationStatus, &empJSON, &custJSON, &p.ProcessedBy, &p.DatabaseSource, &p.CreatedAt,
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
		&p.Identity.VerificationStatus, &empJSON, &custJSON, &p.ProcessedBy, &p.DatabaseSource, &p.CreatedAt,
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

