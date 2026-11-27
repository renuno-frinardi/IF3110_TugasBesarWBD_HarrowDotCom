\set ON_ERROR_STOP on

BEGIN;

SET LOCAL synchronous_commit = off;

INSERT INTO public.regions (id, code, name, continent, currency, tax_rate, routing_weight, is_active, created_at)
VALUES
    (1,  'AP-SE-1',      'Southeast Asia (Jakarta)',    'Asia',          'IDR', 0.1100, 30, TRUE, TIMESTAMPTZ '2024-01-01 00:00:00+00'),
    (2,  'AP-SE-2',      'Southeast Asia (Singapore)',  'Asia',          'SGD', 0.0900, 25, TRUE, TIMESTAMPTZ '2024-01-01 00:00:00+00'),
    (3,  'AP-NE-1',      'Northeast Asia (Tokyo)',      'Asia',          'JPY', 0.1000, 15, TRUE, TIMESTAMPTZ '2024-01-01 00:00:00+00'),
    (4,  'US-EAST-1',    'US East (N. Virginia)',       'North America', 'USD', 0.0725, 20, TRUE, TIMESTAMPTZ '2024-01-01 00:00:00+00'),
    (5,  'US-WEST-2',    'US West (Oregon)',            'North America', 'USD', 0.0850, 15, TRUE, TIMESTAMPTZ '2024-01-01 00:00:00+00'),
    (6,  'EU-CENTRAL-1', 'Europe Central (Frankfurt)',  'Europe',        'EUR', 0.1900, 18, TRUE, TIMESTAMPTZ '2024-01-01 00:00:00+00'),
    (7,  'EU-WEST-1',    'Europe West (Dublin)',        'Europe',        'EUR', 0.2100, 12, TRUE, TIMESTAMPTZ '2024-01-01 00:00:00+00'),
    (8,  'AP-SOUTH-1',   'South Asia (Mumbai)',         'Asia',          'INR', 0.1800, 14, TRUE, TIMESTAMPTZ '2024-01-01 00:00:00+00'),
    (9,  'SA-EAST-1',    'South America (Sao Paulo)',   'South America', 'BRL', 0.1700,  8, TRUE, TIMESTAMPTZ '2024-01-01 00:00:00+00'),
    (10, 'AU-SYD-1',     'Oceania (Sydney)',            'Oceania',       'AUD', 0.1000, 10, TRUE, TIMESTAMPTZ '2024-01-01 00:00:00+00');

INSERT INTO public.merchants (
    id, region_id, merchant_code, business_name, tier, kyc_status,
    commission_bps, risk_score, storefront_config, is_active, onboarded_at
)
SELECT
    g,
    CASE WHEN g % 10 < 4 THEN (g % 2) + 1 ELSE (g % 10) + 1 END,
    'MRC-' || lpad(g::text, 6, '0'),
    'Nexus Merchant Group #' || g || ' ' || (ARRAY['Prime', 'Global', 'Ventures', 'Retail', 'Direct', 'Supply', 'Labs', 'Mart'])[(g % 8) + 1],
    (ARRAY['enterprise', 'growth', 'standard', 'starter'])[
        CASE
            WHEN g <= 100 THEN 1
            WHEN g <= 500 THEN 2
            WHEN g <= 1400 THEN 3
            ELSE 4
        END
    ],
    (ARRAY['verified', 'verified', 'verified', 'pending_review', 'restricted'])[((g * 7) % 5) + 1],
    150 + ((g * 13) % 1100),
    round((((g * 29) % 9200)::numeric / 100.0), 2),
    jsonb_build_object(
        'auto_settlement', (g % 2 = 0),
        'payout_schedule', (ARRAY['daily', 'weekly', 'biweekly'])[(g % 3) + 1],
        'max_concurrent_webhooks', 4 + (g % 16),
        'supported_channels', jsonb_build_array('web', 'mobile_app', CASE WHEN g % 3 = 0 THEN 'b2b_api' ELSE 'pos' END),
        'rate_limit_rps', 50 * ((g % 20) + 1)
    ),
    (g % 25 <> 0),
    TIMESTAMPTZ '2024-01-01 00:00:00+00' + ((g % 500) || ' days')::interval + ((g * 37 % 86400) || ' seconds')::interval
FROM generate_series(1, 2000) AS g;

INSERT INTO public.customers (
    id, region_id, external_uuid, email, full_name, loyalty_tier,
    lifetime_value, credit_limit, segments, preferences, is_active,
    registered_at, last_seen_at
)
SELECT
    g,
    CASE WHEN g % 10 < 5 THEN (g % 3) + 1 ELSE (g % 10) + 1 END,
    md5('harrow-customer-' || g::text)::uuid,
    'customer.' || g || '.' || substr(md5(g::text), 1, 6) || '@commerce.harrow.local',
    (ARRAY['Adi', 'Budi', 'Citra', 'Dewi', 'Eka', 'Fajar', 'Gita', 'Hendra', 'Indah', 'Joko', 'Kirana', 'Laras', 'Maya', 'Nanda', 'Putra', 'Rani'])[(g % 16) + 1]
        || ' ' ||
    (ARRAY['Pratama', 'Wijaya', 'Saputra', 'Kusuma', 'Santoso', 'Hidayat', 'Nugroho', 'Permana', 'Setiawan', 'Utami', 'Halim', 'Gunawan'])[((g * 3) % 12) + 1]
        || ' #' || g,
    CASE
        WHEN g <= 2500 THEN 'vip'
        WHEN g <= 7500 THEN 'platinum'
        WHEN g <= 17500 THEN 'gold'
        WHEN g <= 32500 THEN 'silver'
        ELSE 'bronze'
    END,
    round((CASE WHEN g <= 2500 THEN 25000 + (g * 19 % 95000) ELSE 120 + (g * 31 % 18000) END)::numeric + ((g % 99)::numeric / 100.0), 2),
    round(((1 + (g % 20)) * 1500)::numeric, 2),
    ARRAY[
        (ARRAY['mobile_first', 'desktop_power', 'api_buyer'])[(g % 3) + 1],
        (ARRAY['promo_hunter', 'full_price', 'subscription', 'bulk_order'])[((g * 5) % 4) + 1]
    ],
    jsonb_build_object(
        'locale', (ARRAY['id-ID', 'en-US', 'en-SG', 'ja-JP', 'de-DE'])[(g % 5) + 1],
        'notifications', jsonb_build_object('email', TRUE, 'push', (g % 2 = 0), 'sms', (g % 5 = 0)),
        'saved_payment_tokens', (g % 4) + 1,
        'marketing_opt_in', (g % 3 <> 0)
    ),
    (g % 40 <> 0),
    TIMESTAMPTZ '2024-01-15 00:00:00+00' + ((g % 600) || ' days')::interval,
    TIMESTAMPTZ '2026-06-01 00:00:00+00' + ((g % 175) || ' days')::interval + ((g * 17 % 86400) || ' seconds')::interval
FROM generate_series(1, 50000) AS g;

INSERT INTO public.settlement_batches (
    id, merchant_id, region_id, batch_code, gross_amount, fee_amount, net_amount,
    currency, settlement_status, reconciled, checksum, payload,
    period_start, period_end, updated_at
)
SELECT
    src.g,
    src.m_id,
    src.r_id,
    'STL-' || to_char(src.p_start, 'YYYYMMDD') || '-' || lpad(src.g::text, 8, '0'),
    src.gross,
    src.fee,
    src.gross - src.fee,
    (ARRAY['IDR', 'SGD', 'USD', 'EUR', 'JPY'])[(src.r_id % 5) + 1],
    CASE
        WHEN src.g % 3 = 0 THEN 'settled'
        WHEN src.g % 7 = 0 THEN 'held'
        WHEN src.g % 5 = 0 THEN 'processing'
        ELSE 'pending'
    END,
    (src.g % 3 = 0),
    CASE WHEN src.g % 3 = 0 THEN md5('settled-' || src.g::text) ELSE '' END,
    jsonb_build_object(
        'tx_count', 10 + (src.g % 240),
        'refund_count', src.g % 9,
        'dispute_flag', (src.g % 29 = 0),
        'bank_rail', (ARRAY['BI-FAST', 'SWIFT', 'ACH', 'SEPA', 'FAST'])[(src.r_id % 5) + 1],
        'digest_seed', substr(md5(src.g::text), 1, 16)
    ),
    src.p_start,
    src.p_start + INTERVAL '1 day',
    src.p_start + INTERVAL '26 hours'
FROM (
    SELECT
        g,
        CASE WHEN g % 5 = 0 THEN ((g * 17) % 100) + 1 ELSE ((g * 31) % 2000) + 1 END AS m_id,
        ((g * 7) % 10) + 1 AS r_id,
        round((500 + ((g * 43) % 75000))::numeric + ((g % 100)::numeric / 100.0), 2) AS gross,
        round((12 + ((g * 11) % 1500))::numeric + ((g % 50)::numeric / 100.0), 2) AS fee,
        TIMESTAMPTZ '2025-01-01 00:00:00+00' + ((g % 650) || ' days')::interval + ((g % 24) || ' hours')::interval AS p_start
    FROM generate_series(1, 300000) AS g
) AS src;

INSERT INTO catalog.categories (id, parent_id, slug, name, depth_level, attributes_schema, is_active)
SELECT
    g,
    CASE
        WHEN g <= 12 THEN NULL
        WHEN g <= 60 THEN ((g - 13) % 12) + 1
        ELSE ((g - 61) % 48) + 13
    END,
    'cat-' || lpad(g::text, 3, '0') || '-' || (ARRAY['electronics', 'industrial', 'apparel', 'home', 'automotive', 'health', 'compute', 'networking', 'storage', 'audio', 'gaming', 'office'])[(g % 12) + 1],
    'Category L' || (CASE WHEN g <= 12 THEN '1' WHEN g <= 60 THEN '2' ELSE '3' END) || ' #' || g,
    CASE WHEN g <= 12 THEN 1 WHEN g <= 60 THEN 2 ELSE 3 END,
    jsonb_build_object(
        'requires_serial_number', (g % 4 = 0),
        'hazmat_screening', (g % 9 = 0),
        'warranty_months_default', (ARRAY[6, 12, 24, 36])[(g % 4) + 1]
    ),
    TRUE
FROM generate_series(1, 120) AS g;

INSERT INTO catalog.warehouses (
    id, region_id, warehouse_code, name, tier, max_capacity_units,
    latitude, longitude, operating_profile, is_active
)
SELECT
    g,
    ((g - 1) % 10) + 1,
    'WH-' || lpad(g::text, 3, '0'),
    'Fulfillment Hub #' || g || ' (' || (ARRAY['North', 'South', 'East', 'West', 'Central'])[(g % 5) + 1] || ')',
    (ARRAY['mega_hub', 'regional_dc', 'micro_fulfillment'])[(g % 3) + 1],
    50000 + ((g * 12500) % 450000),
    round((-35.0 + ((g * 17) % 85))::numeric + ((g % 100)::numeric / 1000.0), 5),
    round((-120.0 + ((g * 29) % 260))::numeric + ((g % 100)::numeric / 1000.0), 5),
    jsonb_build_object(
        '24x7_dispatch', (g % 2 = 0),
        'cold_chain_supported', (g % 5 = 0),
        'dock_doors', 8 + (g % 32),
        'automation_level', (ARRAY['robotics_v2', 'conveyor_assisted', 'manual_pick'])[(g % 3) + 1]
    ),
    (g % 30 <> 0)
FROM generate_series(1, 60) AS g;

INSERT INTO catalog.products (
    id, merchant_id, category_id, sku, name, status,
    cost_price, base_price, weight_grams, search_tags, specs,
    created_at, updated_at
)
SELECT
    src.g,
    src.m_id,
    src.c_id,
    'SKU-' || lpad(src.m_id::text, 4, '0') || '-' || lpad(src.g::text, 6, '0'),
    (ARRAY['Ultra', 'Pro', 'Enterprise', 'Compact', 'Smart', 'Modular', 'HeavyDuty', 'Precision'])[(src.g % 8) + 1]
        || ' ' ||
    (ARRAY['Router', 'Sensor', 'Controller', 'Drive', 'Adapter', 'Terminal', 'Monitor', 'Module', 'Battery', 'Chassis'])[((src.g * 3) % 10) + 1]
        || ' X' || src.g,
    (ARRAY['active', 'active', 'active', 'active', 'draft', 'out_of_stock', 'discontinued'])[(src.g % 7) + 1],
    src.cost,
    round(src.cost * (1.18 + ((src.g % 75)::numeric / 100.0)), 2),
    50 + ((src.g * 37) % 24950),
    ARRAY[
        (ARRAY['bestseller', 'new_arrival', 'clearance', 'enterprise_grade'])[(src.g % 4) + 1],
        'cat_' || src.c_id::text,
        (ARRAY['express_eligible', 'fragile', 'oversized', 'standard_ship'])[((src.g * 7) % 4) + 1]
    ],
    jsonb_build_object(
        'brand', 'Brand-' || ((src.g % 250) + 1),
        'power_watts', 5 + (src.g % 750),
        'dimensions_cm', jsonb_build_object('l', 10 + (src.g % 80), 'w', 8 + (src.g % 50), 'h', 4 + (src.g % 40)),
        'origin_country', (ARRAY['ID', 'SG', 'JP', 'DE', 'US', 'VN', 'TW'])[(src.g % 7) + 1],
        'rating_avg', round((3.2 + ((src.g % 18)::numeric / 10.0)), 1)
    ),
    src.ts,
    src.ts + ((src.g % 90) || ' days')::interval
FROM (
    SELECT
        g,
        CASE WHEN g % 4 = 0 THEN ((g * 13) % 100) + 1 ELSE ((g * 41) % 2000) + 1 END AS m_id,
        61 + (g % 60) AS c_id,
        round((8 + ((g * 53) % 2400))::numeric + ((g % 99)::numeric / 100.0), 2) AS cost,
        TIMESTAMPTZ '2024-03-01 00:00:00+00' + ((g % 500) || ' days')::interval AS ts
    FROM generate_series(1, 25000) AS g
) AS src;

INSERT INTO catalog.inventory_levels (
    id, warehouse_id, product_id, on_hand_qty, reserved_qty,
    reorder_point, bin_location, last_counted_at
)
SELECT
    src.g,
    ((src.p_id * 7 + src.slot * 13) % 60) + 1,
    src.p_id,
    src.on_hand,
    LEAST(src.on_hand, (src.g * 11) % 80),
    25 + (src.g % 150),
    chr(65 + (src.g % 18)) || '-' || lpad(((src.g % 40) + 1)::text, 2, '0') || '-' || lpad(((src.g % 12) + 1)::text, 2, '0'),
    TIMESTAMPTZ '2026-09-01 00:00:00+00' + ((src.g % 85) || ' days')::interval + ((src.g * 19 % 86400) || ' seconds')::interval
FROM (
    SELECT
        g,
        ((g - 1) % 25000) + 1 AS p_id,
        div(g - 1, 25000) AS slot,
        (g * 23) % 1200 AS on_hand
    FROM generate_series(1, 100000) AS g
) AS src;

INSERT INTO orders.orders (
    id, customer_id, region_id, warehouse_id, order_number, status,
    payment_method, currency, subtotal, tax_amount, shipping_fee,
    discount_amount, total_amount, shipping_address, placed_at, updated_at
)
SELECT
    src.g,
    src.cust_id,
    src.reg_id,
    src.wh_id,
    'ORD-' || to_char(src.placed, 'YYYYMM') || '-' || lpad(src.g::text, 8, '0'),
    (ARRAY['delivered', 'delivered', 'delivered', 'shipped', 'processing', 'paid', 'pending', 'cancelled', 'refunded'])[(src.g % 9) + 1],
    (ARRAY['virtual_account', 'credit_card', 'qris', 'ewallet', 'wire_transfer'])[(src.g % 5) + 1],
    (ARRAY['IDR', 'SGD', 'JPY', 'USD', 'USD', 'EUR', 'EUR', 'INR', 'BRL', 'AUD'])[src.reg_id],
    src.subtotal,
    src.tax,
    src.ship_fee,
    src.discount,
    src.subtotal + src.tax + src.ship_fee - src.discount,
    jsonb_build_object(
        'recipient', 'Customer #' || src.cust_id,
        'city', (ARRAY['Jakarta', 'Singapore', 'Tokyo', 'Ashburn', 'Portland', 'Frankfurt', 'Dublin', 'Mumbai', 'Sao Paulo', 'Sydney'])[src.reg_id],
        'postal_code', lpad(((10000 + (src.g * 37 % 89999)))::text, 5, '0'),
        'delivery_notes', CASE WHEN src.g % 6 = 0 THEN 'Leave at security desk' ELSE 'Signature required' END,
        'is_commercial_address', (src.g % 4 = 0)
    ),
    src.placed,
    src.placed + ((1 + (src.g % 72)) || ' hours')::interval
FROM (
    SELECT
        g,
        CASE WHEN g % 10 < 3 THEN ((g * 37) % 2500) + 1 ELSE ((g * 73) % 50000) + 1 END AS cust_id,
        CASE WHEN g % 10 < 5 THEN (g % 3) + 1 ELSE (g % 10) + 1 END AS reg_id,
        ((g * 11) % 60) + 1 AS wh_id,
        round((45 + ((g * 67) % 4800))::numeric + ((g % 100)::numeric / 100.0), 2) AS subtotal,
        round((5 + ((g * 7) % 480))::numeric + ((g % 50)::numeric / 100.0), 2) AS tax,
        round((4 + (g % 35))::numeric, 2) AS ship_fee,
        round(((g % 25))::numeric, 2) AS discount,
        TIMESTAMPTZ '2025-01-01 00:00:00+00' + ((g % 680) || ' days')::interval + ((g * 29 % 86400) || ' seconds')::interval AS placed
    FROM generate_series(1, 150000) AS g
) AS src;

INSERT INTO orders.order_items (
    id, order_id, line_no, product_id, merchant_id,
    quantity, unit_price, discount_amount, line_total, fulfillment_status
)
SELECT
    src.g,
    src.ord_id,
    src.line_no,
    src.prod_id,
    src.merch_id,
    src.qty,
    src.u_price,
    src.disc,
    (src.qty * src.u_price) - src.disc,
    (ARRAY['fulfilled', 'fulfilled', 'allocated', 'shipped', 'pending', 'cancelled'])[(src.g % 6) + 1]
FROM (
    SELECT
        g,
        div(g - 1, 3) + 1 AS ord_id,
        ((g - 1) % 3) + 1 AS line_no,
        CASE WHEN g % 5 = 0 THEN ((g * 19) % 500) + 1 ELSE ((g * 97) % 25000) + 1 END AS prod_id,
        CASE WHEN g % 4 = 0 THEN ((g * 13) % 100) + 1 ELSE ((g * 23) % 2000) + 1 END AS merch_id,
        (g % 5) + 1 AS qty,
        round((15 + ((g * 31) % 950))::numeric + ((g % 99)::numeric / 100.0), 2) AS u_price,
        round(((g % 12))::numeric, 2) AS disc
    FROM generate_series(1, 450000) AS g
) AS src;

INSERT INTO orders.shipments (
    id, order_id, warehouse_id, carrier, service_level, tracking_number,
    shipment_status, weight_grams, shipping_cost, route_checkpoints,
    dispatched_at, delivered_at
)
SELECT
    src.g,
    src.g,
    ((src.g * 11) % 60) + 1,
    (ARRAY['JNE', 'SiCepat', 'DHL', 'FedEx', 'NinjaVan', 'Maersk'])[(src.g % 6) + 1],
    (ARRAY['same_day', 'next_day', 'standard', 'freight'])[(src.g % 4) + 1],
    'TRK-' || lpad(src.g::text, 9, '0') || '-' || upper(substr(md5(src.g::text), 1, 6)),
    src.st,
    200 + ((src.g * 43) % 18000),
    round((3.50 + ((src.g * 7) % 65))::numeric, 2),
    jsonb_build_array(
        jsonb_build_object('step', 1, 'event', 'ORIGIN_SCAN', 'hub', 'WH-' || lpad((((src.g * 11) % 60) + 1)::text, 3, '0')),
        jsonb_build_object('step', 2, 'event', 'TRANSIT_SORT', 'hub', 'SORT-' || ((src.g % 12) + 1)),
        jsonb_build_object('step', 3, 'event', upper(src.st), 'sla_met', (src.g % 11 <> 0))
    ),
    src.dispatched,
    CASE WHEN src.st = 'delivered' THEN src.dispatched + ((6 + (src.g % 96)) || ' hours')::interval ELSE NULL END
FROM (
    SELECT
        g,
        (ARRAY['delivered', 'delivered', 'in_transit', 'out_for_delivery', 'label_created', 'exception'])[(g % 6) + 1] AS st,
        TIMESTAMPTZ '2025-01-02 00:00:00+00' + ((g % 680) || ' days')::interval + ((g * 13 % 86400) || ' seconds')::interval AS dispatched
    FROM generate_series(1, 120000) AS g
) AS src;

INSERT INTO finance.ledger_accounts (
    id, merchant_id, account_code, account_name, account_type, currency, current_balance, created_at
)
SELECT
    g,
    NULL,
    'SYS-ACC-' || lpad(g::text, 4, '0'),
    'Platform Ledger Account #' || g || ' (' || (ARRAY['Cash Clearing', 'Escrow Control', 'Fee Revenue', 'Tax Liability', 'Refund Reserve'])[(g % 5) + 1] || ')',
    (ARRAY['asset', 'liability', 'revenue', 'liability', 'expense'])[(g % 5) + 1],
    'USD',
    0,
    TIMESTAMPTZ '2024-01-01 00:00:00+00'
FROM generate_series(1, 20) AS g;

INSERT INTO finance.ledger_accounts (
    id, merchant_id, account_code, account_name, account_type, currency, current_balance, created_at
)
SELECT
    20 + g,
    g,
    'MRC-AR-' || lpad(g::text, 6, '0'),
    'Merchant Receivable #' || g,
    'asset',
    'USD',
    0,
    TIMESTAMPTZ '2024-01-01 00:00:00+00'
FROM generate_series(1, 2000) AS g;

INSERT INTO finance.ledger_accounts (
    id, merchant_id, account_code, account_name, account_type, currency, current_balance, created_at
)
SELECT
    2020 + g,
    g,
    'MRC-AP-' || lpad(g::text, 6, '0'),
    'Merchant Payout Payable #' || g,
    'liability',
    'USD',
    0,
    TIMESTAMPTZ '2024-01-01 00:00:00+00'
FROM generate_series(1, 2000) AS g;

INSERT INTO finance.journal_entries (
    id, order_id, debit_account_id, credit_account_id, entry_type,
    amount, currency, reference_code, reconciled, audit_metadata, posted_at
)
SELECT
    g,
    div(g - 1, 2) + 1,
    (g % 20) + 1,
    21 + (g % 4000),
    (ARRAY['order_capture', 'merchant_escrow', 'fee_recognition', 'settlement_payout', 'refund_reversal'])[(g % 5) + 1],
    round((10 + ((g * 37) % 2500))::numeric + ((g % 100)::numeric / 100.0), 2),
    'USD',
    'JRN-' || lpad(g::text, 9, '0'),
    (g % 2 = 0),
    jsonb_build_object(
        'posting_engine', 'ledger-v3',
        'idempotency_hash', substr(md5('jrn-' || g::text), 1, 20),
        'auto_approved', (g % 10 <> 0)
    ),
    TIMESTAMPTZ '2025-01-01 01:00:00+00' + ((g % 680) || ' days')::interval + ((g * 19 % 86400) || ' seconds')::interval
FROM generate_series(1, 300000) AS g;

WITH net_movements AS (
    SELECT account_id, SUM(delta) AS balance_delta
    FROM (
        SELECT debit_account_id AS account_id, amount AS delta FROM finance.journal_entries
        UNION ALL
        SELECT credit_account_id AS account_id, -amount AS delta FROM finance.journal_entries
    ) m
    GROUP BY account_id
)
UPDATE finance.ledger_accounts la
SET current_balance = nm.balance_delta
FROM net_movements nm
WHERE la.id = nm.account_id;

INSERT INTO finance.fraud_signals (
    id, order_id, customer_id, risk_score, verdict,
    triggered_rules, device_telemetry, evaluated_at
)
SELECT
    g,
    ((g * 13) % 150000) + 1,
    ((g * 29) % 50000) + 1,
    round((((g * 47) % 10000)::numeric / 100.0), 2),
    CASE
        WHEN ((g * 47) % 10000) >= 8800 THEN 'block'
        WHEN ((g * 47) % 10000) >= 7000 THEN 'manual_review'
        WHEN ((g * 47) % 10000) >= 4500 THEN 'challenge_3ds'
        ELSE 'allow'
    END,
    CASE
        WHEN ((g * 47) % 10000) >= 7000 THEN ARRAY['velocity_spike', 'ip_geo_mismatch', 'high_ticket_sku']
        WHEN ((g * 47) % 10000) >= 4500 THEN ARRAY['new_device_fingerprint', 'card_bin_watch']
        ELSE ARRAY['baseline_pass']
    END,
    jsonb_build_object(
        'ip_asn', 'AS' || (1000 + (g % 55000)),
        'tls_ja3', substr(md5('ja3-' || (g % 2500)::text), 1, 16),
        'proxy_detected', (g % 19 = 0),
        'latency_ms', 12 + (g % 340)
    ),
    TIMESTAMPTZ '2025-01-01 00:30:00+00' + ((g % 680) || ' days')::interval + ((g * 23 % 86400) || ' seconds')::interval
FROM generate_series(1, 100000) AS g;

COMMIT;
