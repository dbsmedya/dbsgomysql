CREATE TABLE {{schema}}.`orders` (
    `id` INT NOT NULL AUTO_INCREMENT,
    `customer_email` VARCHAR(100) NOT NULL,
    `amount` DECIMAL(10,2) NOT NULL,
    `is_paid` TINYINT(1) NOT NULL DEFAULT 0,
    `note` VARCHAR(50) DEFAULT 'none',
    `placed_on` DATE DEFAULT (CURRENT_DATE),
    `hidden` INT INVISIBLE DEFAULT NULL,
    `total_cents` INT GENERATED ALWAYS AS (`amount` * 100) STORED,
    `updated_at` TIMESTAMP NULL DEFAULT NULL ON UPDATE CURRENT_TIMESTAMP,
    `region_id` INT DEFAULT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_orders_email` (`customer_email`),
    KEY `idx_orders_amount` (`amount`),
    CONSTRAINT `chk_orders_amount` CHECK (`amount` >= 0)
) ENGINE=InnoDB COMMENT='source ledger';
