CREATE TABLE {{schema}}.`clean_table` (
    `id` INT NOT NULL,
    PRIMARY KEY (`id`)
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`single_unsigned` (
    `id` INT UNSIGNED NOT NULL,
    PRIMARY KEY (`id`)
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`big_pk` (
    `id` BIGINT NOT NULL,
    PRIMARY KEY (`id`)
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`composite_pk` (
    `ordinal_first` INT NOT NULL,
    `key_first` INT NOT NULL,
    PRIMARY KEY (`key_first`, `ordinal_first`)
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`no_pk` (
    `id` INT NOT NULL
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`pk_varchar` (
    `id` VARCHAR(64) NOT NULL,
    PRIMARY KEY (`id`)
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`pk_case_mismatch` (
    `LOG_ID` INT NOT NULL,
    PRIMARY KEY (`LOG_ID`)
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`pk_secondary` (
    `id` INT NOT NULL,
    `other_id` INT NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_secondary` (`id`, `other_id`)
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`expected_mismatch` (
    `actual_id` INT NOT NULL,
    PRIMARY KEY (`actual_id`)
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`invisible_columns` (
    `id` INT NOT NULL,
    `plain_secret` INT INVISIBLE,
    `generated_secret` INT GENERATED ALWAYS AS (`id` + 1) STORED INVISIBLE,
    `visible_generated` INT GENERATED ALWAYS AS (`id` + 2) STORED,
    PRIMARY KEY (`id`)
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`myisam_table` (
    `id` INT NOT NULL,
    PRIMARY KEY (`id`)
) ENGINE=MyISAM;

CREATE TABLE {{schema}}.`delete_trigger` (
    `id` INT NOT NULL,
    PRIMARY KEY (`id`)
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`delete_only_trigger` (
    `id` INT NOT NULL,
    PRIMARY KEY (`id`)
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`fk_parent` (
    `id` INT NOT NULL,
    PRIMARY KEY (`id`)
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`fk_internal_child` (
    `id` INT NOT NULL,
    `parent_id` INT NOT NULL,
    PRIMARY KEY (`id`),
    CONSTRAINT `fk_internal_parent`
        FOREIGN KEY (`parent_id`) REFERENCES {{schema}}.`fk_parent` (`id`)
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`fk_external_child` (
    `id` INT NOT NULL,
    `parent_id` INT NOT NULL,
    PRIMARY KEY (`id`),
    CONSTRAINT `fk_external_parent`
        FOREIGN KEY (`parent_id`) REFERENCES {{schema}}.`fk_parent` (`id`)
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`fk_cascade_child` (
    `id` INT NOT NULL,
    `parent_id` INT NOT NULL,
    PRIMARY KEY (`id`),
    CONSTRAINT `fk_cascade_parent`
        FOREIGN KEY (`parent_id`) REFERENCES {{schema}}.`fk_parent` (`id`)
        ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`fk_composite_parent` (
    `tenant_id` INT NOT NULL,
    `id` INT NOT NULL,
    PRIMARY KEY (`tenant_id`, `id`)
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`fk_composite_child` (
    `id` INT NOT NULL,
    `z` INT NOT NULL,
    `tenant_id` INT,
    `parent_id` INT,
    PRIMARY KEY (`id`),
    KEY `idx_nonleading` (`z`, `tenant_id`, `parent_id`),
    CONSTRAINT `fk_composite_parent`
        FOREIGN KEY (`tenant_id`, `parent_id`)
        REFERENCES {{schema}}.`fk_composite_parent` (`tenant_id`, `id`)
        ON DELETE SET NULL
        ON UPDATE NO ACTION
) ENGINE=InnoDB;

-- A functional key part reports information_schema.STATISTICS.COLUMN_NAME as
-- NULL. Its own table pair, deliberately: fk_composite_child is read by a
-- helper that scans that column into a plain string, so a functional index
-- there would fail for reasons unrelated to what it tests.
-- See docs/COMPAT.md entry 18.
CREATE TABLE {{schema}}.`fk_functional_parent` (
    `id` INT NOT NULL,
    PRIMARY KEY (`id`)
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`fk_functional_child` (
    `id` INT NOT NULL,
    `parent_id` INT NOT NULL,
    `amount` INT NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_functional` ((`amount` * 2)),
    CONSTRAINT `fk_functional_parent`
        FOREIGN KEY (`parent_id`)
        REFERENCES {{schema}}.`fk_functional_parent` (`id`)
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`fk_rule_parent` (
    `id` INT NOT NULL,
    PRIMARY KEY (`id`)
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`fk_rule_cascade` (
    `id` INT NOT NULL,
    `parent_id` INT NOT NULL,
    PRIMARY KEY (`id`),
    CONSTRAINT `fk_rule_cascade`
        FOREIGN KEY (`parent_id`) REFERENCES {{schema}}.`fk_rule_parent` (`id`)
        ON DELETE CASCADE
        ON UPDATE CASCADE
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`fk_rule_set_null` (
    `id` INT NOT NULL,
    `parent_id` INT,
    PRIMARY KEY (`id`),
    CONSTRAINT `fk_rule_set_null`
        FOREIGN KEY (`parent_id`) REFERENCES {{schema}}.`fk_rule_parent` (`id`)
        ON DELETE SET NULL
        ON UPDATE SET NULL
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`fk_rule_no_action` (
    `id` INT NOT NULL,
    `parent_id` INT NOT NULL,
    PRIMARY KEY (`id`),
    CONSTRAINT `fk_rule_no_action`
        FOREIGN KEY (`parent_id`) REFERENCES {{schema}}.`fk_rule_parent` (`id`)
        ON DELETE NO ACTION
        ON UPDATE NO ACTION
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`fk_rule_restrict` (
    `id` INT NOT NULL,
    `parent_id` INT NOT NULL,
    PRIMARY KEY (`id`),
    CONSTRAINT `fk_rule_restrict`
        FOREIGN KEY (`parent_id`) REFERENCES {{schema}}.`fk_rule_parent` (`id`)
        ON DELETE RESTRICT
        ON UPDATE RESTRICT
) ENGINE=InnoDB;

CREATE TABLE {{schema}}.`myisam_ignored_fk` (
    `id` INT NOT NULL,
    `parent_id` INT NOT NULL,
    PRIMARY KEY (`id`),
    FOREIGN KEY (`parent_id`) REFERENCES {{schema}}.`fk_parent` (`id`)
) ENGINE=MyISAM;

CREATE TRIGGER {{schema}}.`ZDeleteAfter`
AFTER DELETE ON {{schema}}.`delete_trigger`
FOR EACH ROW SET @dbsgomysql_delete_after = OLD.`id`;

CREATE TRIGGER {{schema}}.`BDeleteBefore`
BEFORE DELETE ON {{schema}}.`delete_trigger`
FOR EACH ROW SET @dbsgomysql_delete_before_b = OLD.`id`;

CREATE TRIGGER {{schema}}.`ADeleteBefore`
BEFORE DELETE ON {{schema}}.`delete_trigger`
FOR EACH ROW SET @dbsgomysql_delete_before_a = OLD.`id`;

CREATE TRIGGER {{schema}}.`InsertBefore`
BEFORE INSERT ON {{schema}}.`delete_trigger`
FOR EACH ROW SET @dbsgomysql_insert_before = NEW.`id`;

CREATE TRIGGER {{schema}}.`UpdateAfter`
AFTER UPDATE ON {{schema}}.`delete_trigger`
FOR EACH ROW SET @dbsgomysql_update_after = NEW.`id`;

CREATE TRIGGER {{schema}}.`DeleteOnly`
BEFORE DELETE ON {{schema}}.`delete_only_trigger`
FOR EACH ROW SET @dbsgomysql_delete_only = OLD.`id`;

CREATE VIEW {{schema}}.`report_view` AS
SELECT `id` FROM {{schema}}.`clean_table`;
