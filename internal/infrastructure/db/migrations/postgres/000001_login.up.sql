-- Wave 1: rAthena `login` account table (sql-files/main.sql, Thai Classic).
-- PostgreSQL version. goAthena owns the schema via migrations; GORM never
-- AutoMigrates. Identity starts at 2000000 to match rAthena's account-id
-- namespace. enum → char(1) + CHECK (PG has no inline enum).
CREATE TABLE IF NOT EXISTS "login" (
  "account_id" integer GENERATED ALWAYS AS IDENTITY (START WITH 2000000) PRIMARY KEY,
  "userid" varchar(23) NOT NULL DEFAULT '',
  "user_pass" varchar(32) NOT NULL DEFAULT '',
  "sex" char(1) NOT NULL DEFAULT 'M' CHECK ("sex" IN ('M','F','S')),
  "email" varchar(39) NOT NULL DEFAULT '',
  "group_id" smallint NOT NULL DEFAULT 0,
  "state" integer NOT NULL DEFAULT 0,
  "unban_time" integer NOT NULL DEFAULT 0,
  "expiration_time" integer NOT NULL DEFAULT 0,
  "logincount" integer NOT NULL DEFAULT 0,
  "lastlogin" timestamp DEFAULT NULL,
  "last_ip" varchar(100) NOT NULL DEFAULT '',
  "birthdate" date DEFAULT NULL,
  "character_slots" smallint NOT NULL DEFAULT 0,
  "pincode" varchar(4) NOT NULL DEFAULT '',
  "pincode_change" integer NOT NULL DEFAULT 0,
  "vip_time" integer NOT NULL DEFAULT 0,
  "old_group" smallint NOT NULL DEFAULT 0,
  "web_auth_token" varchar(17) DEFAULT NULL,
  "web_auth_token_enabled" smallint NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS "login_userid_idx" ON "login" ("userid");
CREATE UNIQUE INDEX IF NOT EXISTS "login_web_auth_token_key" ON "login" ("web_auth_token");
