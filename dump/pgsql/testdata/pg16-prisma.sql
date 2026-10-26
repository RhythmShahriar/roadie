--
-- PostgreSQL database dump
--

\restrict yi90GZwaNdPdotYQolXPRhak1qReXgWjo8Rwvs7X9JGogspUJKXXMiPZO8rMfn0

-- Dumped from database version 16.14 (Debian 16.14-1.pgdg13+1)
-- Dumped by pg_dump version 16.14 (Debian 16.14-1.pgdg13+1)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: Role; Type: TYPE; Schema: public; Owner: postgres
--

CREATE TYPE public."Role" AS ENUM (
    'USER',
    'ADMIN'
);


ALTER TYPE public."Role" OWNER TO postgres;

--
-- Name: touch_post(); Type: FUNCTION; Schema: public; Owner: postgres
--

CREATE FUNCTION public.touch_post() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    -- a ';' and a 'quote' inside the body
    NEW."title" := trim(NEW."title");
    RETURN NEW;
END;
$$;


ALTER FUNCTION public.touch_post() OWNER TO postgres;

SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: Post; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public."Post" (
    id integer NOT NULL,
    title character varying(255) NOT NULL,
    content text,
    published boolean DEFAULT false NOT NULL,
    "authorId" text NOT NULL,
    slug text GENERATED ALWAYS AS (lower((title)::text)) STORED
);


ALTER TABLE public."Post" OWNER TO postgres;

--
-- Name: Post_id_seq; Type: SEQUENCE; Schema: public; Owner: postgres
--

CREATE SEQUENCE public."Post_id_seq"
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE public."Post_id_seq" OWNER TO postgres;

--
-- Name: Post_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: postgres
--

ALTER SEQUENCE public."Post_id_seq" OWNED BY public."Post".id;


--
-- Name: Session; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public."Session" (
    id uuid NOT NULL,
    "userId" text NOT NULL,
    token bytea NOT NULL,
    data jsonb,
    expires timestamp(6) with time zone NOT NULL
);


ALTER TABLE public."Session" OWNER TO postgres;

--
-- Name: User; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public."User" (
    id text NOT NULL,
    email text NOT NULL,
    name character varying(191),
    role public."Role" DEFAULT 'USER'::public."Role" NOT NULL,
    bio text,
    "createdAt" timestamp(3) without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL
);


ALTER TABLE public."User" OWNER TO postgres;

--
-- Name: _prisma_migrations; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public._prisma_migrations (
    id character varying(36) NOT NULL,
    checksum character varying(64) NOT NULL,
    finished_at timestamp with time zone,
    migration_name character varying(255) NOT NULL,
    logs text,
    applied_steps_count integer DEFAULT 0 NOT NULL
);


ALTER TABLE public._prisma_migrations OWNER TO postgres;

--
-- Name: empty_table; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.empty_table (
    id integer NOT NULL,
    note text
);


ALTER TABLE public.empty_table OWNER TO postgres;

--
-- Name: Post id; Type: DEFAULT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public."Post" ALTER COLUMN id SET DEFAULT nextval('public."Post_id_seq"'::regclass);


--
-- Data for Name: Post; Type: TABLE DATA; Schema: public; Owner: postgres
--

COPY public."Post" (id, title, content, published, "authorId") FROM stdin;
1	Hello; world	Body with -- dashes and /* stars */	t	u1
2	Second	tab\there\nnew line	f	u2
3	$tag$ dollars	\N	t	u1
\.


--
-- Data for Name: Session; Type: TABLE DATA; Schema: public; Owner: postgres
--

COPY public."Session" (id, "userId", token, data, expires) FROM stdin;
00000000-0000-4000-8000-000000000001	u1	\\x00010203ff5c09	{"ip": "192.0.2.1", "ua": "test"}	2025-01-01 00:00:00+00
00000000-0000-4000-8000-000000000002	u2	\\x	\N	2025-01-02 00:00:00+00
\.


--
-- Data for Name: User; Type: TABLE DATA; Schema: public; Owner: postgres
--

COPY public."User" (id, email, name, role, bio, "createdAt") FROM stdin;
u1	ada@example.com	Ada Example	ADMIN	line one\nline two\ttabbed	2024-01-02 03:04:05.678
u2	bob@example.test		USER	back\\slash and \\N literal	2024-02-03 04:05:06
u3	cy@example.com	\N	USER	\N	2024-03-04 05:06:07
u4	dee@example.com	Dée Ünïcode ✓	USER	carriage\rreturn and quote ' here	2024-04-05 06:07:08
\.


--
-- Data for Name: _prisma_migrations; Type: TABLE DATA; Schema: public; Owner: postgres
--

COPY public._prisma_migrations (id, checksum, finished_at, migration_name, logs, applied_steps_count) FROM stdin;
3f1c0e1a-0000-4000-8000-00000000000a	c0ffee	2024-01-01 00:00:00+00	20240101000000_init	\N	1
\.


--
-- Data for Name: empty_table; Type: TABLE DATA; Schema: public; Owner: postgres
--

COPY public.empty_table (id, note) FROM stdin;
\.


--
-- Name: Post_id_seq; Type: SEQUENCE SET; Schema: public; Owner: postgres
--

SELECT pg_catalog.setval('public."Post_id_seq"', 3, true);


--
-- Name: Post Post_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public."Post"
    ADD CONSTRAINT "Post_pkey" PRIMARY KEY (id);


--
-- Name: Session Session_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public."Session"
    ADD CONSTRAINT "Session_pkey" PRIMARY KEY (id);


--
-- Name: Session Session_token_key; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public."Session"
    ADD CONSTRAINT "Session_token_key" UNIQUE (token);


--
-- Name: User User_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public."User"
    ADD CONSTRAINT "User_pkey" PRIMARY KEY (id);


--
-- Name: _prisma_migrations _prisma_migrations_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public._prisma_migrations
    ADD CONSTRAINT _prisma_migrations_pkey PRIMARY KEY (id);


--
-- Name: empty_table empty_table_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.empty_table
    ADD CONSTRAINT empty_table_pkey PRIMARY KEY (id);


--
-- Name: User_email_key; Type: INDEX; Schema: public; Owner: postgres
--

CREATE UNIQUE INDEX "User_email_key" ON public."User" USING btree (email);


--
-- Name: Post post_touch; Type: TRIGGER; Schema: public; Owner: postgres
--

CREATE TRIGGER post_touch BEFORE UPDATE ON public."Post" FOR EACH ROW EXECUTE FUNCTION public.touch_post();


--
-- Name: Post Post_authorId_fkey; Type: FK CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public."Post"
    ADD CONSTRAINT "Post_authorId_fkey" FOREIGN KEY ("authorId") REFERENCES public."User"(id) ON UPDATE CASCADE ON DELETE RESTRICT;


--
-- PostgreSQL database dump complete
--

\unrestrict yi90GZwaNdPdotYQolXPRhak1qReXgWjo8Rwvs7X9JGogspUJKXXMiPZO8rMfn0

