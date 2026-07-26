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
