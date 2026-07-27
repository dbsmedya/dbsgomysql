-- Differs from phase1d_source.orders in exactly these ways:
--   customer_email  VARCHAR(100) -> VARCHAR(200)   ColumnTypeMismatch
--   is_paid         TINYINT(1)   -> TINYINT        ColumnTypeMismatch (BOOLEAN)
--   note            'none'       -> 'n/a'          ColumnDefaultMismatch
--   hidden          INVISIBLE    -> visible        ColumnVisibilityMismatch
--   total_cents     STORED       -> VIRTUAL        ColumnGeneratedMismatch
--   region_id       present      -> absent         ColumnAbsent (SideB)
--   archived_at     absent       -> present        ColumnAbsent (SideA)
--   idx_orders_amount            -> dropped        IndexAbsent (SideB)
--   chk_orders_amount >= 0       -> >= 1           CheckClauseMismatch
--   comment         'source ledger' -> 'archive'   CommentMismatch
CREATE TABLE {{schema}}.`orders` (
    `id` INT NOT NULL AUTO_INCREMENT,
    `customer_email` VARCHAR(200) NOT NULL,
    `amount` DECIMAL(10,2) NOT NULL,
    `is_paid` TINYINT NOT NULL DEFAULT 0,
    `note` VARCHAR(50) DEFAULT 'n/a',
    `placed_on` DATE DEFAULT (CURRENT_DATE),
    `hidden` INT DEFAULT NULL,
    `total_cents` INT GENERATED ALWAYS AS (`amount` * 100) VIRTUAL,
    `updated_at` TIMESTAMP NULL DEFAULT NULL ON UPDATE CURRENT_TIMESTAMP,
    `archived_at` DATETIME DEFAULT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_orders_email` (`customer_email`),
    CONSTRAINT `chk_orders_amount` CHECK (`amount` >= 1)
) ENGINE=InnoDB COMMENT='archive';
