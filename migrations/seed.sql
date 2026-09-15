-- =====================================================================
-- Full seed script — retailer-side schema
-- Run this against an EMPTY dev database (matches your 27 migrations).
-- Uses a DO block with variables so each insert's generated UUID feeds
-- the next one automatically — nothing to copy-paste by hand.
-- Safe to re-run: wrap in a transaction and roll back if you just want
-- to inspect, or run as-is to leave the data in place.
-- =====================================================================

DO $$
DECLARE
    v_retailer_user_id      UUID;
    v_staff_user_id         UUID;
    v_consumer_user_id      UUID;
    v_admin_user_id         UUID;

    v_retailer_id           UUID;

    v_category_parent_id    UUID;
    v_category_child_id     UUID;
    v_brand_id              UUID;

    v_store_id              UUID;

    v_product_1_id          UUID;
    v_product_2_id          UUID;

    v_variant_1_id          UUID;
    v_variant_2_id          UUID;

    v_order_id              UUID;
BEGIN

    -- -----------------------------------------------------------------
    -- USERS (one per user_type we need to exercise)
    -- -----------------------------------------------------------------
    INSERT INTO users (phone, email, password_hash, user_type, status)
    VALUES ('+919810000001', 'retailer.owner@test.com', 'dummy_hash_1', 'RETAILER', 'ACTIVE')
    RETURNING id INTO v_retailer_user_id;

    INSERT INTO users (phone, email, password_hash, user_type, status)
    VALUES ('+919810000002', 'staff.picker@test.com', 'dummy_hash_2', 'STAFF', 'ACTIVE')
    RETURNING id INTO v_staff_user_id;

    INSERT INTO users (phone, email, password_hash, user_type, status)
    VALUES ('+919810000003', 'consumer.one@test.com', 'dummy_hash_3', 'USER', 'ACTIVE')
    RETURNING id INTO v_consumer_user_id;

    INSERT INTO users (phone, email, password_hash, user_type, status)
    VALUES ('+919810000004', 'admin.ops@test.com', 'dummy_hash_4', 'ADMIN', 'ACTIVE')
    RETURNING id INTO v_admin_user_id;

    -- -----------------------------------------------------------------
    -- RETAILER + finance shell
    -- -----------------------------------------------------------------
    INSERT INTO retailers (user_id, legal_name, owner_name, kyc_status)
    VALUES (v_retailer_user_id, 'Sharma Traders Pvt Ltd', 'Ramesh Sharma', 'APPROVED')
    RETURNING id INTO v_retailer_id;

    INSERT INTO retailer_kyc (retailer_id, id_proof_url, business_reg_url, status, reviewed_by, reviewed_at)
    VALUES (v_retailer_id, 'https://example.com/docs/id_proof_1.pdf',
            'https://example.com/docs/biz_reg_1.pdf', 'APPROVED', v_admin_user_id, NOW());

    INSERT INTO bank_details (retailer_id, account_number, ifsc, account_holder_name)
    VALUES (v_retailer_id, '000123456789', 'HDFC0001234', 'Ramesh Sharma');

    INSERT INTO retailer_wallet (retailer_id, balance)
    VALUES (v_retailer_id, 0);

    -- -----------------------------------------------------------------
    -- CATEGORY (parent + subcategory) and BRAND
    -- -----------------------------------------------------------------
    INSERT INTO category (name, is_active)
    VALUES ('Pharmacy', true)
    RETURNING id INTO v_category_parent_id;

    INSERT INTO category (name, parent_category_id, is_active)
    VALUES ('Pain Relief', v_category_parent_id, true)
    RETURNING id INTO v_category_child_id;

    INSERT INTO brand (name, is_active)
    VALUES ('Generic Pharma Co', true)
    RETURNING id INTO v_brand_id;

    -- -----------------------------------------------------------------
    -- STORE + everything attached to it
    -- -----------------------------------------------------------------
    INSERT INTO store (retailer_id, category_id, name, description, status, is_open, kyb_status)
    VALUES (v_retailer_id, v_category_parent_id, 'Sharma Pharmacy - Sector 12',
            'Neighbourhood pharmacy serving Sector 12', 'ACTIVE', true, 'APPROVED')
    RETURNING id INTO v_store_id;

    INSERT INTO store_media (store_id, media_url, type)
    VALUES
        (v_store_id, 'https://example.com/media/store_logo.png', 'logo'),
        (v_store_id, 'https://example.com/media/store_front.jpg', 'photo');

    INSERT INTO store_kyb (store_id, address_proof_url, license_url, status, reviewed_by, reviewed_at)
    VALUES (v_store_id, 'https://example.com/docs/address_proof_1.pdf',
            'https://example.com/docs/license_1.pdf', 'APPROVED', v_admin_user_id, NOW());

    INSERT INTO store_hours (store_id, day_of_week, open_time, close_time)
    VALUES
        (v_store_id, 'MONDAY', '09:00', '21:00'),
        (v_store_id, 'TUESDAY', '09:00', '21:00'),
        (v_store_id, 'WEDNESDAY', '09:00', '21:00'),
        (v_store_id, 'THURSDAY', '09:00', '21:00'),
        (v_store_id, 'FRIDAY', '09:00', '21:00'),
        (v_store_id, 'SATURDAY', '09:00', '21:00'),
        (v_store_id, 'SUNDAY', '10:00', '18:00');

    INSERT INTO store_location (store_id, address_line, city, pincode, lat, lng, service_area_radius_km)
    VALUES (v_store_id, '12 MG Road, Sector 12', 'Jamshedpur', '831001', 22.804400, 86.203300, 5);

    -- -----------------------------------------------------------------
    -- STAFF
    -- -----------------------------------------------------------------
    INSERT INTO staff_member (user_id, store_id, role, invited_by, is_active)
    VALUES (v_staff_user_id, v_store_id, 'picker', v_retailer_user_id, true);

    -- -----------------------------------------------------------------
    -- PRODUCTS + variants + images + inventory
    -- -----------------------------------------------------------------
    INSERT INTO product (store_id, category_id, brand_id, name, description, attributes, status)
    VALUES (v_store_id, v_category_child_id, v_brand_id,
            'Paracetamol 500mg Tablets', 'Fever and pain relief tablets, strip of 10',
            '{"dosage": "500mg", "form": "tablet", "prescription_required": false}'::jsonb, 'ACTIVE')
    RETURNING id INTO v_product_1_id;

    INSERT INTO product (store_id, category_id, brand_id, name, description, attributes, status)
    VALUES (v_store_id, v_category_child_id, v_brand_id,
            'Paracetamol Syrup for Kids', 'Sugar-free paracetamol syrup, 60ml bottle',
            '{"dosage": "120mg/5ml", "form": "syrup", "prescription_required": false}'::jsonb, 'ACTIVE')
    RETURNING id INTO v_product_2_id;

    INSERT INTO product_image (product_id, image_url, sort_order)
    VALUES
        (v_product_1_id, 'https://example.com/products/paracetamol_tab_1.jpg', 0),
        (v_product_2_id, 'https://example.com/products/paracetamol_syrup_1.jpg', 0);

    INSERT INTO product_variant (product_id, variant_label, sku, price)
    VALUES (v_product_1_id, 'Strip of 10', 'PARA-TAB-500-10', 25.00)
    RETURNING id INTO v_variant_1_id;

    INSERT INTO product_variant (product_id, variant_label, sku, price)
    VALUES (v_product_2_id, '60ml Bottle', 'PARA-SYR-60ML', 45.00)
    RETURNING id INTO v_variant_2_id;

    INSERT INTO inventory (product_variant_id, store_id, quantity_available, quantity_reserved, low_stock_threshold)
    VALUES
        (v_variant_1_id, v_store_id, 100, 0, 10),
        (v_variant_2_id, v_store_id, 40, 0, 5);

    -- -----------------------------------------------------------------
    -- ORDER + status history + items
    -- -----------------------------------------------------------------
    INSERT INTO orders (consumer_id, store_id, status, delivery_mode, total_amount, payment_status)
    VALUES (v_consumer_user_id, v_store_id, 'PLACED', 'delivery', 70.00, 'PAID')
    RETURNING id INTO v_order_id;

    INSERT INTO order_status_history (order_id, status, changed_by)
    VALUES (v_order_id, 'PLACED', v_consumer_user_id);

    INSERT INTO order_item (order_id, product_variant_id, quantity, price_at_order_time, substitution_status)
    VALUES
        (v_order_id, v_variant_1_id, 1, 25.00, 'NONE'),
        (v_order_id, v_variant_2_id, 1, 45.00, 'NONE');

    -- Reflect the reservation this order would trigger in real code
    UPDATE inventory SET quantity_available = quantity_available - 1, quantity_reserved = quantity_reserved + 1
    WHERE product_variant_id = v_variant_1_id AND store_id = v_store_id;
    UPDATE inventory SET quantity_available = quantity_available - 1, quantity_reserved = quantity_reserved + 1
    WHERE product_variant_id = v_variant_2_id AND store_id = v_store_id;

    -- -----------------------------------------------------------------
    -- FINANCE — one ledger entry for this order's payment
    -- -----------------------------------------------------------------
    INSERT INTO ledger_entry (retailer_id, store_id, amount, type)
    VALUES (v_retailer_id, v_store_id, 70.00, 'order_payment');

    -- -----------------------------------------------------------------
    -- VERIFICATION REQUEST — an example admin follow-up
    -- -----------------------------------------------------------------
    INSERT INTO verification_request (target_type, target_id, requested_info, status, created_by)
    VALUES ('store', v_store_id, 'Please re-upload a clearer photo of the shop license', 'open', v_admin_user_id);

    -- -----------------------------------------------------------------
    -- REVIEW — one product review, one store-only review
    -- -----------------------------------------------------------------
    INSERT INTO review (store_id, product_id, consumer_id, rating, comment)
    VALUES (v_store_id, v_product_1_id, v_consumer_user_id, 5, 'Fast delivery, genuine product.');

    INSERT INTO review (store_id, product_id, consumer_id, rating, comment)
    VALUES (v_store_id, NULL, v_consumer_user_id, 4, 'Good packaging, slightly delayed pickup.');

    -- -----------------------------------------------------------------
    -- SUPPORT TICKET + evidence
    -- -----------------------------------------------------------------
    DECLARE
        v_ticket_id UUID;
    BEGIN
        INSERT INTO support_ticket (store_id, order_id, raised_by, category, status)
        VALUES (v_store_id, v_order_id, v_consumer_user_id, 'delivery_issue', 'open')
        RETURNING id INTO v_ticket_id;

        INSERT INTO evidence (ticket_id, file_url)
        VALUES (v_ticket_id, 'https://example.com/evidence/photo_1.jpg');
    END;

    -- -----------------------------------------------------------------
    -- SETTLEMENT — one past weekly payout, already released
    -- -----------------------------------------------------------------
    INSERT INTO settlement (retailer_id, amount, period_start, period_end, status)
    VALUES (v_retailer_id, 500.00, CURRENT_DATE - INTERVAL '7 days', CURRENT_DATE - INTERVAL '1 day', 'APPROVED');

    RAISE NOTICE 'Seed complete. store_id=%, product_1=%, product_2=%, order_id=%',
        v_store_id, v_product_1_id, v_product_2_id, v_order_id;

END $$;