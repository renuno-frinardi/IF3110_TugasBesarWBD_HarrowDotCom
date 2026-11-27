\set ON_ERROR_STOP on

CREATE SCHEMA IF NOT EXISTS catalog;
CREATE SCHEMA IF NOT EXISTS orders;
CREATE SCHEMA IF NOT EXISTS finance;

CREATE TABLE public.regions (
    id SMALLINT PRIMARY KEY,
    code VARCHAR(20) NOT NULL,
    name VARCHAR(100) NOT NULL,
    continent VARCHAR(30) NOT NULL,
    currency CHAR(3) NOT NULL,
    tax_rate NUMERIC(5,4) NOT NULL,
    routing_weight SMALLINT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE public.merchants (
    id BIGINT PRIMARY KEY,
    region_id SMALLINT NOT NULL,
    merchant_code VARCHAR(32) NOT NULL,
    business_name VARCHAR(150) NOT NULL,
    tier VARCHAR(20) NOT NULL,
    kyc_status VARCHAR(20) NOT NULL,
    commission_bps INTEGER NOT NULL,
    risk_score NUMERIC(5,2) NOT NULL,
    storefront_config JSONB NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    onboarded_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE public.customers (
    id BIGINT PRIMARY KEY,
    region_id SMALLINT NOT NULL,
    external_uuid UUID NOT NULL,
    email VARCHAR(180) NOT NULL,
    full_name VARCHAR(120) NOT NULL,
    loyalty_tier VARCHAR(20) NOT NULL,
    lifetime_value NUMERIC(14,2) NOT NULL DEFAULT 0,
    credit_limit NUMERIC(12,2) NOT NULL DEFAULT 0,
    segments TEXT[] NOT NULL,
    preferences JSONB NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    registered_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE public.settlement_batches (
    id BIGINT PRIMARY KEY,
    merchant_id BIGINT NOT NULL,
    region_id SMALLINT NOT NULL,
    batch_code VARCHAR(40) NOT NULL,
    gross_amount NUMERIC(14,2) NOT NULL,
    fee_amount NUMERIC(14,2) NOT NULL,
    net_amount NUMERIC(14,2) NOT NULL,
    currency CHAR(3) NOT NULL,
    settlement_status VARCHAR(20) NOT NULL,
    reconciled BOOLEAN NOT NULL DEFAULT FALSE,
    checksum VARCHAR(64) NOT NULL DEFAULT '',
    payload JSONB NOT NULL,
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE catalog.categories (
    id INTEGER PRIMARY KEY,
    parent_id INTEGER NULL,
    slug VARCHAR(80) NOT NULL,
    name VARCHAR(120) NOT NULL,
    depth_level SMALLINT NOT NULL,
    attributes_schema JSONB NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE catalog.warehouses (
    id INTEGER PRIMARY KEY,
    region_id SMALLINT NOT NULL,
    warehouse_code VARCHAR(24) NOT NULL,
    name VARCHAR(120) NOT NULL,
    tier VARCHAR(20) NOT NULL,
    max_capacity_units INTEGER NOT NULL,
    latitude NUMERIC(8,5) NOT NULL,
    longitude NUMERIC(8,5) NOT NULL,
    operating_profile JSONB NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE catalog.products (
    id BIGINT PRIMARY KEY,
    merchant_id BIGINT NOT NULL,
    category_id INTEGER NOT NULL,
    sku VARCHAR(40) NOT NULL,
    name VARCHAR(180) NOT NULL,
    status VARCHAR(20) NOT NULL,
    cost_price NUMERIC(12,2) NOT NULL,
    base_price NUMERIC(12,2) NOT NULL,
    weight_grams INTEGER NOT NULL,
    search_tags TEXT[] NOT NULL,
    specs JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE catalog.inventory_levels (
    id BIGINT PRIMARY KEY,
    warehouse_id INTEGER NOT NULL,
    product_id BIGINT NOT NULL,
    on_hand_qty INTEGER NOT NULL,
    reserved_qty INTEGER NOT NULL,
    reorder_point INTEGER NOT NULL,
    bin_location VARCHAR(24) NOT NULL,
    last_counted_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE orders.orders (
    id BIGINT PRIMARY KEY,
    customer_id BIGINT NOT NULL,
    region_id SMALLINT NOT NULL,
    warehouse_id INTEGER NOT NULL,
    order_number VARCHAR(36) NOT NULL,
    status VARCHAR(20) NOT NULL,
    payment_method VARCHAR(24) NOT NULL,
    currency CHAR(3) NOT NULL,
    subtotal NUMERIC(14,2) NOT NULL,
    tax_amount NUMERIC(14,2) NOT NULL,
    shipping_fee NUMERIC(14,2) NOT NULL,
    discount_amount NUMERIC(14,2) NOT NULL,
    total_amount NUMERIC(14,2) NOT NULL,
    shipping_address JSONB NOT NULL,
    placed_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE orders.order_items (
    id BIGINT PRIMARY KEY,
    order_id BIGINT NOT NULL,
    line_no SMALLINT NOT NULL,
    product_id BIGINT NOT NULL,
    merchant_id BIGINT NOT NULL,
    quantity INTEGER NOT NULL,
    unit_price NUMERIC(12,2) NOT NULL,
    discount_amount NUMERIC(12,2) NOT NULL,
    line_total NUMERIC(14,2) NOT NULL,
    fulfillment_status VARCHAR(20) NOT NULL
);

CREATE TABLE orders.shipments (
    id BIGINT PRIMARY KEY,
    order_id BIGINT NOT NULL,
    warehouse_id INTEGER NOT NULL,
    carrier VARCHAR(30) NOT NULL,
    service_level VARCHAR(20) NOT NULL,
    tracking_number VARCHAR(40) NOT NULL,
    shipment_status VARCHAR(20) NOT NULL,
    weight_grams INTEGER NOT NULL,
    shipping_cost NUMERIC(10,2) NOT NULL,
    route_checkpoints JSONB NOT NULL,
    dispatched_at TIMESTAMPTZ NOT NULL,
    delivered_at TIMESTAMPTZ NULL
);

CREATE TABLE finance.ledger_accounts (
    id BIGINT PRIMARY KEY,
    merchant_id BIGINT NULL,
    account_code VARCHAR(32) NOT NULL,
    account_name VARCHAR(140) NOT NULL,
    account_type VARCHAR(20) NOT NULL,
    currency CHAR(3) NOT NULL,
    current_balance NUMERIC(16,2) NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE finance.journal_entries (
    id BIGINT PRIMARY KEY,
    order_id BIGINT NOT NULL,
    debit_account_id BIGINT NOT NULL,
    credit_account_id BIGINT NOT NULL,
    entry_type VARCHAR(24) NOT NULL,
    amount NUMERIC(14,2) NOT NULL,
    currency CHAR(3) NOT NULL,
    reference_code VARCHAR(48) NOT NULL,
    reconciled BOOLEAN NOT NULL DEFAULT FALSE,
    audit_metadata JSONB NOT NULL,
    posted_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE finance.fraud_signals (
    id BIGINT PRIMARY KEY,
    order_id BIGINT NOT NULL,
    customer_id BIGINT NOT NULL,
    risk_score NUMERIC(5,2) NOT NULL,
    verdict VARCHAR(20) NOT NULL,
    triggered_rules TEXT[] NOT NULL,
    device_telemetry JSONB NOT NULL,
    evaluated_at TIMESTAMPTZ NOT NULL
);
