CREATE TABLE IF NOT EXISTS parties (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id VARCHAR(64) NOT NULL,
    party_type VARCHAR(32) NOT NULL,
    full_name VARCHAR(255) NOT NULL,
    email VARCHAR(255) NOT NULL,
    phone VARCHAR(64) NOT NULL,
    identity_type VARCHAR(32) NOT NULL,
    identity_number VARCHAR(64) NOT NULL UNIQUE,
    identity_issued_date VARCHAR(64) NOT NULL DEFAULT '',
    identity_issued_place VARCHAR(255) NOT NULL DEFAULT '',
    verification_status VARCHAR(64) NOT NULL,
    employee_profile JSONB,
    customer_profile JSONB,
    processed_by VARCHAR(128) NOT NULL,
    tax_code VARCHAR(64) NOT NULL DEFAULT '',
    kyc_notes TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO parties (
    id, tenant_id, party_type, full_name, email, phone,
    identity_type, identity_number, identity_issued_date, identity_issued_place,
    verification_status, employee_profile, processed_by
) VALUES (
    'PRT-SEED-01', current_database(), 'EMPLOYEE', 'Nguyễn Văn Mẫu (Seeded)', 'seed@domain.com', '0901234567',
    'CCCD', '001090123456', '2022-01-01', 'Cục CSQLHC về TTXH',
    'EKYC_CHIP_VERIFIED', '{"employee_code":"EMP-001","department":"Information Technology","position":"Software Engineer"}'::jsonb,
    'seed_script'
) ON CONFLICT (identity_number) DO NOTHING;

