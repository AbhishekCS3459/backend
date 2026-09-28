-- Expand step of the quantity_available -> on_hand_quantity rename.
-- The legacy columns stay and are kept in sync by a trigger so a backend that
-- still uses them keeps working. A later contract migration drops them.

ALTER TABLE inventory
    ADD COLUMN IF NOT EXISTS on_hand_quantity INTEGER,
    ADD COLUMN IF NOT EXISTS reserved_quantity INTEGER,
    ADD COLUMN IF NOT EXISTS listed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS unlisted_at TIMESTAMPTZ;

UPDATE inventory
SET on_hand_quantity = quantity_available,
    reserved_quantity = quantity_reserved,
    listed_at = COALESCE(listed_at, updated_at)
WHERE on_hand_quantity IS NULL OR reserved_quantity IS NULL OR listed_at IS NULL;

-- No defaults on the new stock columns while the legacy columns exist: a NULL on
-- insert tells the trigger the row came from code that only knows the legacy names.
ALTER TABLE inventory
    ALTER COLUMN on_hand_quantity SET NOT NULL,
    ALTER COLUMN reserved_quantity SET NOT NULL,
    ALTER COLUMN listed_at SET DEFAULT NOW(),
    ALTER COLUMN listed_at SET NOT NULL;

ALTER TABLE inventory DROP CONSTRAINT IF EXISTS inventory_stock_check;
ALTER TABLE inventory ADD CONSTRAINT inventory_stock_check CHECK (
    on_hand_quantity >= 0
    AND reserved_quantity >= 0
    AND reserved_quantity <= on_hand_quantity
);

CREATE OR REPLACE FUNCTION inventory_sync_legacy_stock() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.on_hand_quantity IS NULL THEN
            NEW.on_hand_quantity := COALESCE(NEW.quantity_available, 0);
        END IF;
        IF NEW.reserved_quantity IS NULL THEN
            NEW.reserved_quantity := COALESCE(NEW.quantity_reserved, 0);
        END IF;
    ELSE
        IF NEW.on_hand_quantity IS NOT DISTINCT FROM OLD.on_hand_quantity
           AND NEW.quantity_available IS DISTINCT FROM OLD.quantity_available THEN
            NEW.on_hand_quantity := NEW.quantity_available;
        END IF;
        IF NEW.reserved_quantity IS NOT DISTINCT FROM OLD.reserved_quantity
           AND NEW.quantity_reserved IS DISTINCT FROM OLD.quantity_reserved THEN
            NEW.reserved_quantity := NEW.quantity_reserved;
        END IF;
    END IF;
    NEW.quantity_available := NEW.on_hand_quantity;
    NEW.quantity_reserved := NEW.reserved_quantity;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS inventory_sync_legacy_stock ON inventory;
CREATE TRIGGER inventory_sync_legacy_stock
    BEFORE INSERT OR UPDATE ON inventory
    FOR EACH ROW EXECUTE FUNCTION inventory_sync_legacy_stock();

CREATE INDEX IF NOT EXISTS idx_inventory_store_listed ON inventory (store_id) WHERE unlisted_at IS NULL;

CREATE TABLE IF NOT EXISTS inventory_transaction (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- seq is assigned at insert time, after the inventory row lock is taken, so
    -- it orders a product's history in the order changes were applied.
    seq BIGINT GENERATED ALWAYS AS IDENTITY UNIQUE,
    inventory_id UUID NOT NULL REFERENCES inventory(id),
    store_id UUID NOT NULL REFERENCES store(id),
    product_variant_id UUID NOT NULL REFERENCES product_variant(id),
    type VARCHAR(32) NOT NULL
        CHECK (type IN ('OPENING_BALANCE', 'STOCK_RECEIVED', 'ADJUSTMENT')),
    reason VARCHAR(32)
        CHECK (reason IN ('DAMAGED', 'EXPIRED', 'LOST', 'STOCK_COUNT', 'RETURN_TO_SUPPLIER', 'OTHER')),
    quantity INTEGER NOT NULL,
    before_on_hand INTEGER NOT NULL,
    after_on_hand INTEGER NOT NULL,
    before_reserved INTEGER NOT NULL,
    after_reserved INTEGER NOT NULL,
    counted_quantity INTEGER,
    reference VARCHAR(100),
    note VARCHAR(500),
    batch_id UUID,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT inventory_transaction_math CHECK (after_on_hand = before_on_hand + quantity),
    CONSTRAINT inventory_transaction_stock CHECK (
        before_on_hand >= 0 AND after_on_hand >= 0
        AND before_reserved >= 0 AND after_reserved >= 0
        AND after_reserved <= after_on_hand
    ),
    CONSTRAINT inventory_transaction_reason CHECK ((type = 'ADJUSTMENT') = (reason IS NOT NULL)),
    CONSTRAINT inventory_transaction_count CHECK (
        (reason IS NOT DISTINCT FROM 'STOCK_COUNT') = (counted_quantity IS NOT NULL)
        AND (counted_quantity IS NULL OR counted_quantity = after_on_hand)
    ),
    CONSTRAINT inventory_transaction_quantity CHECK (
        (type = 'STOCK_RECEIVED' AND quantity > 0)
        OR (type = 'OPENING_BALANCE' AND quantity >= 0)
        OR (type = 'ADJUSTMENT' AND (quantity <> 0 OR reason = 'STOCK_COUNT'))
    )
);

CREATE INDEX IF NOT EXISTS idx_inventory_transaction_inventory ON inventory_transaction (inventory_id, seq DESC);
CREATE INDEX IF NOT EXISTS idx_inventory_transaction_store ON inventory_transaction (store_id, seq DESC);
CREATE INDEX IF NOT EXISTS idx_inventory_transaction_batch ON inventory_transaction (batch_id) WHERE batch_id IS NOT NULL;

-- Stores the first successful response per key so a retried request replays it
-- instead of changing stock again.
CREATE TABLE IF NOT EXISTS inventory_idempotency (
    store_id UUID NOT NULL REFERENCES store(id),
    idempotency_key VARCHAR(100) NOT NULL,
    request_hash CHAR(64) NOT NULL,
    response JSONB NOT NULL,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (store_id, idempotency_key)
);

-- Existing stock becomes an opening balance so every history sums to on_hand.
INSERT INTO inventory_transaction (
    inventory_id, store_id, product_variant_id, type, quantity,
    before_on_hand, after_on_hand, before_reserved, after_reserved, note, created_at
)
SELECT i.id, i.store_id, i.product_variant_id, 'OPENING_BALANCE', i.on_hand_quantity,
       0, i.on_hand_quantity, i.reserved_quantity, i.reserved_quantity,
       'Stock before inventory history was introduced', i.updated_at
FROM inventory i
WHERE i.on_hand_quantity > 0
  AND NOT EXISTS (SELECT 1 FROM inventory_transaction t WHERE t.inventory_id = i.id);
