-- Đối tác (nhân viên, khách hàng) kèm giấy tờ tuỳ thân
CREATE TABLE IF NOT EXISTS parties (
    id                    text        NOT NULL,
    store_id              uuid        NOT NULL DEFAULT platform.store_id(),
    tenant_id             text        NOT NULL,
    party_type            text        NOT NULL,
    full_name             text        NOT NULL,
    email                 text        NOT NULL,
    phone                 text        NOT NULL,
    identity_type         text        NOT NULL,
    identity_number       text        NOT NULL,
    identity_issued_date  text        NOT NULL DEFAULT '',
    identity_issued_place text        NOT NULL DEFAULT '',
    verification_status   text        NOT NULL,
    employee_profile      jsonb,
    customer_profile      jsonb,
    processed_by          text        NOT NULL,
    tax_code              text        NOT NULL DEFAULT '',
    kyc_notes             text        NOT NULL DEFAULT '',
    created_at            timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (store_id, id),
    UNIQUE (store_id, identity_number)
);
