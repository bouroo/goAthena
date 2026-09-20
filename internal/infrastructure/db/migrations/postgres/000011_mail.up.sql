-- Wave 11: rAthena mail tables (postgres variant). See the MariaDB file for
-- shape rationale: RODEX core columns only; random options / enchantgrade are
-- not modeled in goAthena yet.
CREATE TABLE IF NOT EXISTS "mail" (
  "id" BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  "send_name" varchar(30) NOT NULL DEFAULT '',
  "send_id" INT NOT NULL DEFAULT 0,
  "dest_name" varchar(30) NOT NULL DEFAULT '',
  "dest_id" INT NOT NULL DEFAULT 0,
  "title" varchar(45) NOT NULL DEFAULT '',
  "message" varchar(500) NOT NULL DEFAULT '',
  "time" INT NOT NULL DEFAULT 0,
  "status" SMALLINT NOT NULL DEFAULT 0,
  "zeny" BIGINT NOT NULL DEFAULT 0,
  "type" SMALLINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS "mail_dest_id_idx" ON "mail" ("dest_id");

CREATE TABLE IF NOT EXISTS "mail_attachments" (
  "id" BIGINT NOT NULL,
  "index" SMALLINT NOT NULL DEFAULT 0,
  "nameid" INT NOT NULL DEFAULT 0,
  "amount" INT NOT NULL DEFAULT 0,
  "refine" SMALLINT NOT NULL DEFAULT 0,
  "attribute" SMALLINT NOT NULL DEFAULT 0,
  "identify" SMALLINT NOT NULL DEFAULT 0,
  "card0" INT NOT NULL DEFAULT 0,
  "card1" INT NOT NULL DEFAULT 0,
  "card2" INT NOT NULL DEFAULT 0,
  "card3" INT NOT NULL DEFAULT 0,
  "unique_id" BIGINT NOT NULL DEFAULT 0,
  "bound" SMALLINT NOT NULL DEFAULT 0,
  "timestamp" INT NOT NULL DEFAULT 0,
  PRIMARY KEY ("id", "index"),
  CONSTRAINT "mail_id_fk" FOREIGN KEY ("id") REFERENCES "mail" ("id")
    ON DELETE CASCADE ON UPDATE CASCADE
);
