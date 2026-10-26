-- A Prisma-style schema with fictional rows, the input of gen-pg16.sh.
-- Every address is on example.com or example.test (RFC 2606).
CREATE TYPE "Role" AS ENUM ('USER', 'ADMIN');

CREATE TABLE "User" (
    "id" TEXT NOT NULL,
    "email" TEXT NOT NULL,
    "name" VARCHAR(191),
    "role" "Role" NOT NULL DEFAULT 'USER',
    "bio" TEXT,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT "User_pkey" PRIMARY KEY ("id")
);
CREATE UNIQUE INDEX "User_email_key" ON "User"("email");

CREATE TABLE "Post" (
    "id" SERIAL NOT NULL,
    "title" CHARACTER VARYING(255) NOT NULL,
    "content" TEXT,
    "published" BOOLEAN NOT NULL DEFAULT false,
    "authorId" TEXT NOT NULL,
    "slug" TEXT GENERATED ALWAYS AS (lower("title")) STORED,
    CONSTRAINT "Post_pkey" PRIMARY KEY ("id")
);
ALTER TABLE "Post" ADD CONSTRAINT "Post_authorId_fkey" FOREIGN KEY ("authorId") REFERENCES "User"("id") ON DELETE RESTRICT ON UPDATE CASCADE;

CREATE TABLE "Session" (
    "id" UUID NOT NULL,
    "userId" TEXT NOT NULL,
    "token" BYTEA NOT NULL,
    "data" JSONB,
    "expires" TIMESTAMPTZ(6) NOT NULL,
    CONSTRAINT "Session_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "Session_token_key" UNIQUE ("token")
);

CREATE TABLE "_prisma_migrations" (
    "id" VARCHAR(36) NOT NULL,
    "checksum" VARCHAR(64) NOT NULL,
    "finished_at" TIMESTAMPTZ,
    "migration_name" VARCHAR(255) NOT NULL,
    "logs" TEXT,
    "applied_steps_count" INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT "_prisma_migrations_pkey" PRIMARY KEY ("id")
);

CREATE TABLE empty_table (id integer PRIMARY KEY, note text);

CREATE FUNCTION touch_post() RETURNS trigger LANGUAGE plpgsql AS $body$
BEGIN
    -- a ';' and a 'quote' inside the body
    NEW."title" := trim(NEW."title");
    RETURN NEW;
END;
$body$;
CREATE TRIGGER post_touch BEFORE UPDATE ON "Post" FOR EACH ROW EXECUTE FUNCTION touch_post();

INSERT INTO "User" VALUES
  ('u1', 'ada@example.com', 'Ada Example', 'ADMIN', E'line one\nline two\ttabbed', '2024-01-02 03:04:05.678'),
  ('u2', 'bob@example.test', '', 'USER', E'back\\slash and \\N literal', '2024-02-03 04:05:06'),
  ('u3', 'cy@example.com', NULL, 'USER', NULL, '2024-03-04 05:06:07'),
  ('u4', 'dee@example.com', 'Dée Ünïcode ✓', 'USER', E'carriage\rreturn and quote '' here', '2024-04-05 06:07:08');
INSERT INTO "Post" ("title", "content", "published", "authorId") VALUES
  ('Hello; world', 'Body with -- dashes and /* stars */', true, 'u1'),
  ('Second', E'tab\there\nnew line', false, 'u2'),
  ('$tag$ dollars', NULL, true, 'u1');
INSERT INTO "Session" VALUES
  ('00000000-0000-4000-8000-000000000001', 'u1', '\x00010203ff5c09', '{"ip": "192.0.2.1", "ua": "test"}', '2025-01-01 00:00:00+00'),
  ('00000000-0000-4000-8000-000000000002', 'u2', '\x', NULL, '2025-01-02 00:00:00+00');
INSERT INTO "_prisma_migrations" VALUES
  ('3f1c0e1a-0000-4000-8000-00000000000a', 'c0ffee', '2024-01-01 00:00:00+00', '20240101000000_init', NULL, 1);
