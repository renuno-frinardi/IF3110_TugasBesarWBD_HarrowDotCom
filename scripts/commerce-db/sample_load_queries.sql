WITH regional_vip_orders AS (
    SELECT
        o.id AS order_id,
        o.region_id,
        o.customer_id,
        o.total_amount,
        o.placed_at,
        c.loyalty_tier
    FROM orders.orders o
    JOIN public.customers c ON c.id = o.customer_id
    WHERE c.loyalty_tier IN ('vip', 'platinum', 'gold')
      AND o.status IN ('delivered', 'shipped', 'paid')
      AND o.placed_at >= TIMESTAMPTZ '2025-06-01 00:00:00+00'
)
SELECT
    r.code AS region,
    m.tier AS merchant_tier,
    COUNT(DISTINCT rvo.order_id) AS high_value_orders,
    SUM(oi.line_total) AS merchant_revenue,
    ROUND(AVG(fs.risk_score), 2) AS avg_fraud_score,
    RANK() OVER (PARTITION BY r.code ORDER BY SUM(oi.line_total) DESC) AS tier_rank_in_region
FROM regional_vip_orders rvo
JOIN public.regions r ON r.id = rvo.region_id
JOIN orders.order_items oi ON oi.order_id = rvo.order_id
JOIN public.merchants m ON m.id = oi.merchant_id
LEFT JOIN finance.fraud_signals fs ON fs.order_id = rvo.order_id
WHERE (m.storefront_config ->> 'auto_settlement')::boolean = TRUE
GROUP BY r.code, m.tier
ORDER BY r.code, merchant_revenue DESC;

SELECT
    w.warehouse_code,
    p.sku,
    p.name,
    il.on_hand_qty,
    il.reserved_qty,
    il.reorder_point,
    p.specs ->> 'origin_country' AS origin_country
FROM catalog.inventory_levels il
JOIN catalog.warehouses w ON w.id = il.warehouse_id
JOIN catalog.products p ON p.id = il.product_id
WHERE il.on_hand_qty <= il.reorder_point
  AND p.search_tags @> ARRAY['enterprise_grade']
  AND p.specs @> '{"origin_country": "JP"}'::jsonb
ORDER BY (il.reorder_point - il.on_hand_qty) DESC
LIMIT 100;
