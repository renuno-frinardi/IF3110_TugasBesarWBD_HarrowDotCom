\set ON_ERROR_STOP on

SET maintenance_work_mem = '256MB';

ALTER TABLE public.regions            ADD CONSTRAINT uq_regions_code UNIQUE (code);
ALTER TABLE public.merchants          ADD CONSTRAINT uq_merchants_code UNIQUE (merchant_code);
ALTER TABLE public.customers          ADD CONSTRAINT uq_customers_uuid UNIQUE (external_uuid);
ALTER TABLE public.customers          ADD CONSTRAINT uq_customers_email UNIQUE (email);
ALTER TABLE public.settlement_batches ADD CONSTRAINT uq_settlement_batches_code UNIQUE (batch_code);

ALTER TABLE catalog.categories        ADD CONSTRAINT uq_categories_slug UNIQUE (slug);
ALTER TABLE catalog.warehouses        ADD CONSTRAINT uq_warehouses_code UNIQUE (warehouse_code);
ALTER TABLE catalog.products          ADD CONSTRAINT uq_products_sku UNIQUE (sku);
ALTER TABLE catalog.inventory_levels  ADD CONSTRAINT uq_inventory_wh_product UNIQUE (warehouse_id, product_id);

ALTER TABLE orders.orders             ADD CONSTRAINT uq_orders_number UNIQUE (order_number);
ALTER TABLE orders.order_items        ADD CONSTRAINT uq_order_items_line UNIQUE (order_id, line_no);
ALTER TABLE orders.shipments          ADD CONSTRAINT uq_shipments_tracking UNIQUE (tracking_number);

ALTER TABLE finance.ledger_accounts   ADD CONSTRAINT uq_ledger_accounts_code UNIQUE (account_code);
ALTER TABLE finance.journal_entries   ADD CONSTRAINT uq_journal_entries_ref UNIQUE (reference_code);

ALTER TABLE public.regions
    ADD CONSTRAINT chk_regions_tax_weight
    CHECK (tax_rate >= 0 AND tax_rate <= 1 AND routing_weight > 0);

ALTER TABLE public.merchants
    ADD CONSTRAINT chk_merchants_commission_risk
    CHECK (commission_bps BETWEEN 0 AND 10000 AND risk_score BETWEEN 0 AND 100);

ALTER TABLE public.settlement_batches
    ADD CONSTRAINT chk_settlement_amounts
    CHECK (gross_amount >= 0 AND fee_amount >= 0 AND net_amount = gross_amount - fee_amount);

ALTER TABLE catalog.categories
    ADD CONSTRAINT chk_categories_depth
    CHECK (depth_level BETWEEN 1 AND 5);

ALTER TABLE catalog.products
    ADD CONSTRAINT chk_products_pricing_weight
    CHECK (cost_price > 0 AND base_price >= cost_price AND weight_grams > 0);

ALTER TABLE catalog.inventory_levels
    ADD CONSTRAINT chk_inventory_quantities
    CHECK (on_hand_qty >= 0 AND reserved_qty >= 0 AND reserved_qty <= on_hand_qty);

ALTER TABLE orders.orders
    ADD CONSTRAINT chk_orders_totals
    CHECK (total_amount = subtotal + tax_amount + shipping_fee - discount_amount AND total_amount >= 0);

ALTER TABLE orders.order_items
    ADD CONSTRAINT chk_order_items_totals
    CHECK (quantity > 0 AND line_total = (quantity * unit_price) - discount_amount AND line_total >= 0);

ALTER TABLE finance.journal_entries
    ADD CONSTRAINT chk_journal_double_entry
    CHECK (amount > 0 AND debit_account_id <> credit_account_id);

ALTER TABLE finance.fraud_signals
    ADD CONSTRAINT chk_fraud_risk_bounds
    CHECK (risk_score BETWEEN 0 AND 100);

ALTER TABLE public.merchants
    ADD CONSTRAINT fk_merchants_region FOREIGN KEY (region_id) REFERENCES public.regions(id);

ALTER TABLE public.customers
    ADD CONSTRAINT fk_customers_region FOREIGN KEY (region_id) REFERENCES public.regions(id);

ALTER TABLE public.settlement_batches
    ADD CONSTRAINT fk_settlement_merchant FOREIGN KEY (merchant_id) REFERENCES public.merchants(id),
    ADD CONSTRAINT fk_settlement_region   FOREIGN KEY (region_id)   REFERENCES public.regions(id);

ALTER TABLE catalog.categories
    ADD CONSTRAINT fk_categories_parent FOREIGN KEY (parent_id) REFERENCES catalog.categories(id);

ALTER TABLE catalog.warehouses
    ADD CONSTRAINT fk_warehouses_region FOREIGN KEY (region_id) REFERENCES public.regions(id);

ALTER TABLE catalog.products
    ADD CONSTRAINT fk_products_merchant FOREIGN KEY (merchant_id) REFERENCES public.merchants(id),
    ADD CONSTRAINT fk_products_category FOREIGN KEY (category_id) REFERENCES catalog.categories(id);

ALTER TABLE catalog.inventory_levels
    ADD CONSTRAINT fk_inventory_warehouse FOREIGN KEY (warehouse_id) REFERENCES catalog.warehouses(id),
    ADD CONSTRAINT fk_inventory_product   FOREIGN KEY (product_id)   REFERENCES catalog.products(id);

ALTER TABLE orders.orders
    ADD CONSTRAINT fk_orders_customer  FOREIGN KEY (customer_id)  REFERENCES public.customers(id),
    ADD CONSTRAINT fk_orders_region    FOREIGN KEY (region_id)    REFERENCES public.regions(id),
    ADD CONSTRAINT fk_orders_warehouse FOREIGN KEY (warehouse_id) REFERENCES catalog.warehouses(id);

ALTER TABLE orders.order_items
    ADD CONSTRAINT fk_order_items_order    FOREIGN KEY (order_id)    REFERENCES orders.orders(id) ON DELETE CASCADE,
    ADD CONSTRAINT fk_order_items_product  FOREIGN KEY (product_id)  REFERENCES catalog.products(id),
    ADD CONSTRAINT fk_order_items_merchant FOREIGN KEY (merchant_id) REFERENCES public.merchants(id);

ALTER TABLE orders.shipments
    ADD CONSTRAINT fk_shipments_order     FOREIGN KEY (order_id)     REFERENCES orders.orders(id) ON DELETE CASCADE,
    ADD CONSTRAINT fk_shipments_warehouse FOREIGN KEY (warehouse_id) REFERENCES catalog.warehouses(id);

ALTER TABLE finance.ledger_accounts
    ADD CONSTRAINT fk_ledger_accounts_merchant FOREIGN KEY (merchant_id) REFERENCES public.merchants(id);

ALTER TABLE finance.journal_entries
    ADD CONSTRAINT fk_journal_order  FOREIGN KEY (order_id)          REFERENCES orders.orders(id),
    ADD CONSTRAINT fk_journal_debit  FOREIGN KEY (debit_account_id)  REFERENCES finance.ledger_accounts(id),
    ADD CONSTRAINT fk_journal_credit FOREIGN KEY (credit_account_id) REFERENCES finance.ledger_accounts(id);

ALTER TABLE finance.fraud_signals
    ADD CONSTRAINT fk_fraud_order    FOREIGN KEY (order_id)    REFERENCES orders.orders(id) ON DELETE CASCADE,
    ADD CONSTRAINT fk_fraud_customer FOREIGN KEY (customer_id) REFERENCES public.customers(id);

CREATE INDEX idx_merchants_region_tier       ON public.merchants (region_id, tier) WHERE is_active = TRUE;
CREATE INDEX idx_customers_region_loyalty    ON public.customers (region_id, loyalty_tier) INCLUDE (lifetime_value);
CREATE INDEX idx_customers_segments_gin      ON public.customers USING GIN (segments);
CREATE INDEX idx_settlement_merchant_status  ON public.settlement_batches (merchant_id, settlement_status);
CREATE INDEX idx_settlement_unreconciled     ON public.settlement_batches (id) WHERE reconciled = FALSE;
CREATE INDEX idx_settlement_period_brin      ON public.settlement_batches USING BRIN (period_start);

CREATE INDEX idx_categories_parent           ON catalog.categories (parent_id);
CREATE INDEX idx_products_merchant_status    ON catalog.products (merchant_id, status);
CREATE INDEX idx_products_category_price     ON catalog.products (category_id, base_price DESC);
CREATE INDEX idx_products_tags_gin           ON catalog.products USING GIN (search_tags);
CREATE INDEX idx_products_specs_gin          ON catalog.products USING GIN (specs jsonb_path_ops);
CREATE INDEX idx_inventory_low_stock         ON catalog.inventory_levels (warehouse_id, on_hand_qty) WHERE on_hand_qty <= reorder_point;
CREATE INDEX idx_inventory_product           ON catalog.inventory_levels (product_id);

CREATE INDEX idx_orders_customer_placed      ON orders.orders (customer_id, placed_at DESC);
CREATE INDEX idx_orders_region_status        ON orders.orders (region_id, status) INCLUDE (total_amount);
CREATE INDEX idx_orders_placed_brin          ON orders.orders USING BRIN (placed_at);
CREATE INDEX idx_order_items_product         ON orders.order_items (product_id);
CREATE INDEX idx_order_items_merchant_status ON orders.order_items (merchant_id, fulfillment_status) INCLUDE (line_total);
CREATE INDEX idx_shipments_order             ON orders.shipments (order_id);
CREATE INDEX idx_shipments_carrier_status    ON orders.shipments (carrier, shipment_status);

-- Finance indexes
CREATE INDEX idx_ledger_accounts_merchant    ON finance.ledger_accounts (merchant_id) WHERE merchant_id IS NOT NULL;
CREATE INDEX idx_journal_order               ON finance.journal_entries (order_id);
CREATE INDEX idx_journal_debit_posted        ON finance.journal_entries (debit_account_id, posted_at DESC);
CREATE INDEX idx_journal_credit_posted       ON finance.journal_entries (credit_account_id, posted_at DESC);
CREATE INDEX idx_journal_unreconciled        ON finance.journal_entries (id) WHERE reconciled = FALSE;
CREATE INDEX idx_fraud_high_risk             ON finance.fraud_signals (verdict, risk_score DESC) WHERE risk_score >= 70;
CREATE INDEX idx_fraud_customer_eval         ON finance.fraud_signals (customer_id, evaluated_at DESC);

CREATE OR REPLACE VIEW finance.v_merchant_revenue_reconciliation AS
SELECT
    m.id AS merchant_id,
    m.merchant_code,
    m.business_name,
    m.tier,
    r.code AS region_code,
    COUNT(DISTINCT oi.order_id) AS total_orders,
    COALESCE(SUM(oi.line_total), 0) AS gross_item_gmv,
    COALESCE((
        SELECT SUM(sb.net_amount)
        FROM public.settlement_batches sb
        WHERE sb.merchant_id = m.id AND sb.reconciled = TRUE
    ), 0) AS reconciled_payouts
FROM public.merchants m
JOIN public.regions r ON r.id = m.region_id
LEFT JOIN orders.order_items oi ON oi.merchant_id = m.id AND oi.fulfillment_status = 'fulfilled'
GROUP BY m.id, m.merchant_code, m.business_name, m.tier, r.code;

CREATE OR REPLACE VIEW orders.v_regional_fulfillment_sla AS
SELECT
    r.code AS region_code,
    w.warehouse_code,
    s.carrier,
    s.service_level,
    COUNT(*) AS total_shipments,
    COUNT(*) FILTER (WHERE s.shipment_status = 'delivered') AS delivered_count,
    ROUND(AVG(EXTRACT(EPOCH FROM (s.delivered_at - s.dispatched_at)) / 3600.0)::numeric, 2) AS avg_delivery_hours
FROM orders.shipments s
JOIN catalog.warehouses w ON w.id = s.warehouse_id
JOIN public.regions r ON r.id = w.region_id
GROUP BY r.code, w.warehouse_code, s.carrier, s.service_level;

ANALYZE;
