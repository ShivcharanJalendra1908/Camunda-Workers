--
-- PostgreSQL database dump
--

\restrict PepclzoHeiuspAmEthEDSbUhHYQNGKzBCl2cCdeid9UgXXvTifUDZrnFMeTkXoo

-- Dumped from database version 15.15
-- Dumped by pg_dump version 15.15

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

SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: applications; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.applications (
    id character varying(255) NOT NULL,
    seeker_id character varying(255) NOT NULL,
    franchise_id character varying(255) NOT NULL,
    application_data jsonb,
    readiness_score integer,
    priority character varying(50),
    status character varying(50),
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP
);


ALTER TABLE public.applications OWNER TO postgres;

--
-- Name: audit_log; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.audit_log (
    id integer NOT NULL,
    event_type character varying(100),
    resource_type character varying(100),
    resource_id character varying(255),
    details jsonb,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP
);


ALTER TABLE public.audit_log OWNER TO postgres;

--
-- Name: audit_log_id_seq; Type: SEQUENCE; Schema: public; Owner: postgres
--

CREATE SEQUENCE public.audit_log_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER TABLE public.audit_log_id_seq OWNER TO postgres;

--
-- Name: audit_log_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: postgres
--

ALTER SEQUENCE public.audit_log_id_seq OWNED BY public.audit_log.id;


--
-- Name: auth_sessions; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.auth_sessions (
    id character varying(255) NOT NULL,
    user_id character varying(255) NOT NULL,
    token character varying(255) NOT NULL,
    expires_at timestamp without time zone,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP
);


ALTER TABLE public.auth_sessions OWNER TO postgres;

--
-- Name: franchise_outlets; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.franchise_outlets (
    id integer NOT NULL,
    franchise_id character varying(255),
    address text,
    city character varying(100),
    state character varying(100),
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP
);


ALTER TABLE public.franchise_outlets OWNER TO postgres;

--
-- Name: franchise_outlets_id_seq; Type: SEQUENCE; Schema: public; Owner: postgres
--

CREATE SEQUENCE public.franchise_outlets_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER TABLE public.franchise_outlets_id_seq OWNER TO postgres;

--
-- Name: franchise_outlets_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: postgres
--

ALTER SEQUENCE public.franchise_outlets_id_seq OWNED BY public.franchise_outlets.id;


--
-- Name: franchises; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.franchises (
    id character varying(255) NOT NULL,
    name character varying(255) NOT NULL,
    description text,
    investment_min integer,
    investment_max integer,
    category character varying(100),
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP
);


ALTER TABLE public.franchises OWNER TO postgres;

--
-- Name: franchisors; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.franchisors (
    id integer NOT NULL,
    franchise_id character varying(255),
    account_type character varying(50) DEFAULT 'standard'::character varying,
    email character varying(255),
    phone character varying(50),
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP
);


ALTER TABLE public.franchisors OWNER TO postgres;

--
-- Name: franchisors_id_seq; Type: SEQUENCE; Schema: public; Owner: postgres
--

CREATE SEQUENCE public.franchisors_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER TABLE public.franchisors_id_seq OWNER TO postgres;

--
-- Name: franchisors_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: postgres
--

ALTER SEQUENCE public.franchisors_id_seq OWNED BY public.franchisors.id;


--
-- Name: user_subscriptions; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.user_subscriptions (
    id integer NOT NULL,
    user_id character varying(255) NOT NULL,
    tier character varying(50) NOT NULL,
    expires_at timestamp without time zone,
    is_valid boolean DEFAULT true,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP
);


ALTER TABLE public.user_subscriptions OWNER TO postgres;

--
-- Name: user_subscriptions_id_seq; Type: SEQUENCE; Schema: public; Owner: postgres
--

CREATE SEQUENCE public.user_subscriptions_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER TABLE public.user_subscriptions_id_seq OWNER TO postgres;

--
-- Name: user_subscriptions_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: postgres
--

ALTER SEQUENCE public.user_subscriptions_id_seq OWNED BY public.user_subscriptions.id;


--
-- Name: users; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.users (
    id character varying(255) NOT NULL,
    email character varying(255) NOT NULL,
    phone character varying(50),
    capital_available integer,
    location_preferences jsonb,
    interests jsonb,
    industry_experience integer,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP
);


ALTER TABLE public.users OWNER TO postgres;

--
-- Name: zoho_contacts; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.zoho_contacts (
    id character varying(255) NOT NULL,
    email character varying(255) NOT NULL,
    first_name character varying(100),
    last_name character varying(100),
    phone character varying(50),
    company character varying(255),
    lead_source character varying(100),
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP
);


ALTER TABLE public.zoho_contacts OWNER TO postgres;

--
-- Name: audit_log id; Type: DEFAULT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.audit_log ALTER COLUMN id SET DEFAULT nextval('public.audit_log_id_seq'::regclass);


--
-- Name: franchise_outlets id; Type: DEFAULT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.franchise_outlets ALTER COLUMN id SET DEFAULT nextval('public.franchise_outlets_id_seq'::regclass);


--
-- Name: franchisors id; Type: DEFAULT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.franchisors ALTER COLUMN id SET DEFAULT nextval('public.franchisors_id_seq'::regclass);


--
-- Name: user_subscriptions id; Type: DEFAULT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.user_subscriptions ALTER COLUMN id SET DEFAULT nextval('public.user_subscriptions_id_seq'::regclass);


--
-- Data for Name: applications; Type: TABLE DATA; Schema: public; Owner: postgres
--

COPY public.applications (id, seeker_id, franchise_id, application_data, readiness_score, priority, status, created_at, updated_at) FROM stdin;
bab9cb89-cbbb-4df6-86dc-714da6993928	test-user-1765995126555918700	test-franchise-1765995126555918700	null	0		submitted	2025-12-17 18:12:06	2025-12-17 18:12:06
55db6437-56c4-4b57-b056-94393b813730	test-user-1765999829182018800	test-franchise-1765999829182018800	null	0		submitted	2025-12-17 19:30:29	2025-12-17 19:30:29
5d376363-347b-4463-8d0a-d3cb8a3349ad	test-user-1766002771312508600	test-franchise-1766002771312508600	null	0		submitted	2025-12-17 20:19:31	2025-12-17 20:19:31
7d7043e5-9bba-48a9-8de0-ef37bb6f5b85	test-user-1766033046694805000	test-franchise-1766033046694805000	null	0		submitted	2025-12-18 04:44:06	2025-12-18 04:44:06
f9efc161-b1bd-4915-bc5d-8a428534ccbe	test-user-1766047058628327100	test-franchise-1766047058628327100	null	0		submitted	2025-12-18 08:37:38	2025-12-18 08:37:38
da53bf5e-965f-4a3e-8909-a9acb1fffa95	test-user-1766047058727115500	test-franchise-1766047058727115500	null	0		submitted	2025-12-18 08:37:38	2025-12-18 08:37:38
4cb8773a-cf84-4ad1-a8f4-c00fa1e6e2e6	test-user-1766049454552579100	test-franchise-1766049454552579100	null	0		submitted	2025-12-18 09:17:34	2025-12-18 09:17:34
ee36adf8-b2b2-47d5-877c-463c0fc1be52	test-user-1766055156178775800	test-franchise-1766055156178775800	null	0		submitted	2025-12-18 10:52:36	2025-12-18 10:52:36
5a99dbc4-ba27-48d5-9571-b6b7498ae2a1	test-user-1766055156395978600	test-franchise-1766055156395978600	null	0		submitted	2025-12-18 10:52:36	2025-12-18 10:52:36
fc81b103-e4df-4d3e-afc1-f4c1a4c078b4	test-user-1766056254790147600	test-franchise-1766056254790147600	null	0		submitted	2025-12-18 11:10:54	2025-12-18 11:10:54
73521a04-8634-41f5-9932-368495a47e10	test-user-1766056255919538200	test-franchise-1766056255919538200	null	0		submitted	2025-12-18 11:10:56	2025-12-18 11:10:56
5054fb55-77a3-4f7c-81fb-ac572c81c085	test-user-1766058120162673500	test-franchise-1766058120162673500	null	0		submitted	2025-12-18 11:42:00	2025-12-18 11:42:00
6cdcaa60-de1e-428f-9d00-025820e27b9b	test-user-1766058572679759000	test-franchise-1766058572679759000	null	0		submitted	2025-12-18 11:49:32	2025-12-18 11:49:32
334a47d7-14c6-464a-ab25-8450b440fd5c	test-user-1766059482610614500	test-franchise-1766059482610614500	null	0		submitted	2025-12-18 12:04:42	2025-12-18 12:04:42
ead2802e-e40d-4d74-a827-0e521da571f9	test-user-1766059719968026500	test-franchise-1766059719968026500	null	0		submitted	2025-12-18 12:08:40	2025-12-18 12:08:40
074d9882-18a6-423e-85f6-30ec2f1ae6ac	test-user-1766059988966536700	test-franchise-1766059988966536700	null	0		submitted	2025-12-18 12:13:08	2025-12-18 12:13:08
1e6388af-954c-4023-b9dd-52d1bd2f1530	test-user-1766060190846892600	test-franchise-1766060190846892600	null	0		submitted	2025-12-18 12:16:30	2025-12-18 12:16:30
e0f9659c-6e39-48e9-a1cd-b78ce68a5057	test-user-1766060404927024300	test-franchise-1766060404927024300	null	0		submitted	2025-12-18 12:20:04	2025-12-18 12:20:04
9ed76a16-ffbd-4eee-a794-73e6b5b50934	test-user-1766060746647978000	test-franchise-1766060746647978000	null	0		submitted	2025-12-18 12:25:46	2025-12-18 12:25:46
1bee359d-3bc2-43e0-a191-20ef6aac5347	test-user-1766068196137787300	test-franchise-1766068196137787300	null	0		submitted	2025-12-18 14:29:56	2025-12-18 14:29:56
4b58ace9-0262-4558-b43e-631250c5b63e	test-user-1766069238160345100	test-franchise-1766069238160345100	null	0		submitted	2025-12-18 14:47:18	2025-12-18 14:47:18
5a475ddf-ad60-4589-8502-e8ae1bf49b0c	test-user-1766069238798240500	test-franchise-1766069238798240500	null	0		submitted	2025-12-18 14:47:18	2025-12-18 14:47:18
7cc872e4-0b13-4e5f-a7f5-8c3a18086e17	test-user-1766071394006174700	test-franchise-1766071394006174700	null	0		submitted	2025-12-18 15:23:14	2025-12-18 15:23:14
50b9ca71-591c-457c-88c5-e3ae605e53f6	test-user-1766071394726273900	test-franchise-1766071394726273900	null	0		submitted	2025-12-18 15:23:14	2025-12-18 15:23:14
990530f5-ad20-40f9-ab04-6bacd11dd3e8	test-user-1766071619793205300	test-franchise-1766071619793205300	null	0		submitted	2025-12-18 15:26:59	2025-12-18 15:26:59
0bd50338-bd22-40fe-9300-c2681c20b6ad	test-user-1766071622197223200	test-franchise-1766071622197223200	null	0		submitted	2025-12-18 15:27:02	2025-12-18 15:27:02
af31e91b-7a19-4007-966c-40fec387e986	test-user-1766072225696934400	test-franchise-1766072225696934400	null	0		submitted	2025-12-18 15:37:05	2025-12-18 15:37:05
ea9a1ddc-7699-4540-ad9d-0711bf855214	test-user-1766072564680627100	test-franchise-1766072564680627100	null	0		submitted	2025-12-18 15:42:44	2025-12-18 15:42:44
c227e78d-2a3a-4208-ba28-ca1398299881	test-user-1766080439911096700	test-franchise-1766080439911096700	null	0		submitted	2025-12-18 17:53:59	2025-12-18 17:53:59
03591bda-09da-40fc-aa0f-f9d8b70a210a	test-user-1766080439912207000	test-franchise-1766080439912207000	null	0		submitted	2025-12-18 17:53:59	2025-12-18 17:53:59
\.


--
-- Data for Name: audit_log; Type: TABLE DATA; Schema: public; Owner: postgres
--

COPY public.audit_log (id, event_type, resource_type, resource_id, details, created_at) FROM stdin;
1	application_created	application	bab9cb89-cbbb-4df6-86dc-714da6993928	{"priority": "", "seekerId": "test-user-1765995126555918700", "franchiseId": "test-franchise-1765995126555918700", "readinessScore": 0}	2025-12-17 18:12:06
2	application_created	application	55db6437-56c4-4b57-b056-94393b813730	{"priority": "", "seekerId": "test-user-1765999829182018800", "franchiseId": "test-franchise-1765999829182018800", "readinessScore": 0}	2025-12-17 19:30:29
3	application_created	application	5d376363-347b-4463-8d0a-d3cb8a3349ad	{"priority": "", "seekerId": "test-user-1766002771312508600", "franchiseId": "test-franchise-1766002771312508600", "readinessScore": 0}	2025-12-17 20:19:31
4	application_created	application	7d7043e5-9bba-48a9-8de0-ef37bb6f5b85	{"priority": "", "seekerId": "test-user-1766033046694805000", "franchiseId": "test-franchise-1766033046694805000", "readinessScore": 0}	2025-12-18 04:44:06
5	application_created	application	f9efc161-b1bd-4915-bc5d-8a428534ccbe	{"priority": "", "seekerId": "test-user-1766047058628327100", "franchiseId": "test-franchise-1766047058628327100", "readinessScore": 0}	2025-12-18 08:37:38
6	application_created	application	da53bf5e-965f-4a3e-8909-a9acb1fffa95	{"priority": "", "seekerId": "test-user-1766047058727115500", "franchiseId": "test-franchise-1766047058727115500", "readinessScore": 0}	2025-12-18 08:37:38
7	application_created	application	4cb8773a-cf84-4ad1-a8f4-c00fa1e6e2e6	{"priority": "", "seekerId": "test-user-1766049454552579100", "franchiseId": "test-franchise-1766049454552579100", "readinessScore": 0}	2025-12-18 09:17:34
8	application_created	application	ee36adf8-b2b2-47d5-877c-463c0fc1be52	{"priority": "", "seekerId": "test-user-1766055156178775800", "franchiseId": "test-franchise-1766055156178775800", "readinessScore": 0}	2025-12-18 10:52:36
9	application_created	application	5a99dbc4-ba27-48d5-9571-b6b7498ae2a1	{"priority": "", "seekerId": "test-user-1766055156395978600", "franchiseId": "test-franchise-1766055156395978600", "readinessScore": 0}	2025-12-18 10:52:36
10	application_created	application	fc81b103-e4df-4d3e-afc1-f4c1a4c078b4	{"priority": "", "seekerId": "test-user-1766056254790147600", "franchiseId": "test-franchise-1766056254790147600", "readinessScore": 0}	2025-12-18 11:10:54
11	application_created	application	73521a04-8634-41f5-9932-368495a47e10	{"priority": "", "seekerId": "test-user-1766056255919538200", "franchiseId": "test-franchise-1766056255919538200", "readinessScore": 0}	2025-12-18 11:10:56
12	application_created	application	5054fb55-77a3-4f7c-81fb-ac572c81c085	{"priority": "", "seekerId": "test-user-1766058120162673500", "franchiseId": "test-franchise-1766058120162673500", "readinessScore": 0}	2025-12-18 11:42:00
13	application_created	application	6cdcaa60-de1e-428f-9d00-025820e27b9b	{"priority": "", "seekerId": "test-user-1766058572679759000", "franchiseId": "test-franchise-1766058572679759000", "readinessScore": 0}	2025-12-18 11:49:32
14	application_created	application	334a47d7-14c6-464a-ab25-8450b440fd5c	{"priority": "", "seekerId": "test-user-1766059482610614500", "franchiseId": "test-franchise-1766059482610614500", "readinessScore": 0}	2025-12-18 12:04:42
15	application_created	application	ead2802e-e40d-4d74-a827-0e521da571f9	{"priority": "", "seekerId": "test-user-1766059719968026500", "franchiseId": "test-franchise-1766059719968026500", "readinessScore": 0}	2025-12-18 12:08:40
16	application_created	application	074d9882-18a6-423e-85f6-30ec2f1ae6ac	{"priority": "", "seekerId": "test-user-1766059988966536700", "franchiseId": "test-franchise-1766059988966536700", "readinessScore": 0}	2025-12-18 12:13:08
17	application_created	application	1e6388af-954c-4023-b9dd-52d1bd2f1530	{"priority": "", "seekerId": "test-user-1766060190846892600", "franchiseId": "test-franchise-1766060190846892600", "readinessScore": 0}	2025-12-18 12:16:30
18	application_created	application	e0f9659c-6e39-48e9-a1cd-b78ce68a5057	{"priority": "", "seekerId": "test-user-1766060404927024300", "franchiseId": "test-franchise-1766060404927024300", "readinessScore": 0}	2025-12-18 12:20:04
19	application_created	application	9ed76a16-ffbd-4eee-a794-73e6b5b50934	{"priority": "", "seekerId": "test-user-1766060746647978000", "franchiseId": "test-franchise-1766060746647978000", "readinessScore": 0}	2025-12-18 12:25:46
20	application_created	application	1bee359d-3bc2-43e0-a191-20ef6aac5347	{"priority": "", "seekerId": "test-user-1766068196137787300", "franchiseId": "test-franchise-1766068196137787300", "readinessScore": 0}	2025-12-18 14:29:56
21	application_created	application	4b58ace9-0262-4558-b43e-631250c5b63e	{"priority": "", "seekerId": "test-user-1766069238160345100", "franchiseId": "test-franchise-1766069238160345100", "readinessScore": 0}	2025-12-18 14:47:18
22	application_created	application	5a475ddf-ad60-4589-8502-e8ae1bf49b0c	{"priority": "", "seekerId": "test-user-1766069238798240500", "franchiseId": "test-franchise-1766069238798240500", "readinessScore": 0}	2025-12-18 14:47:18
23	application_created	application	7cc872e4-0b13-4e5f-a7f5-8c3a18086e17	{"priority": "", "seekerId": "test-user-1766071394006174700", "franchiseId": "test-franchise-1766071394006174700", "readinessScore": 0}	2025-12-18 15:23:14
24	application_created	application	50b9ca71-591c-457c-88c5-e3ae605e53f6	{"priority": "", "seekerId": "test-user-1766071394726273900", "franchiseId": "test-franchise-1766071394726273900", "readinessScore": 0}	2025-12-18 15:23:14
25	application_created	application	990530f5-ad20-40f9-ab04-6bacd11dd3e8	{"priority": "", "seekerId": "test-user-1766071619793205300", "franchiseId": "test-franchise-1766071619793205300", "readinessScore": 0}	2025-12-18 15:26:59
26	application_created	application	0bd50338-bd22-40fe-9300-c2681c20b6ad	{"priority": "", "seekerId": "test-user-1766071622197223200", "franchiseId": "test-franchise-1766071622197223200", "readinessScore": 0}	2025-12-18 15:27:02
27	application_created	application	af31e91b-7a19-4007-966c-40fec387e986	{"priority": "", "seekerId": "test-user-1766072225696934400", "franchiseId": "test-franchise-1766072225696934400", "readinessScore": 0}	2025-12-18 15:37:05
28	application_created	application	ea9a1ddc-7699-4540-ad9d-0711bf855214	{"priority": "", "seekerId": "test-user-1766072564680627100", "franchiseId": "test-franchise-1766072564680627100", "readinessScore": 0}	2025-12-18 15:42:44
29	application_created	application	c227e78d-2a3a-4208-ba28-ca1398299881	{"priority": "", "seekerId": "test-user-1766080439911096700", "franchiseId": "test-franchise-1766080439911096700", "readinessScore": 0}	2025-12-18 17:53:59
30	application_created	application	03591bda-09da-40fc-aa0f-f9d8b70a210a	{"priority": "", "seekerId": "test-user-1766080439912207000", "franchiseId": "test-franchise-1766080439912207000", "readinessScore": 0}	2025-12-18 17:53:59
\.


--
-- Data for Name: auth_sessions; Type: TABLE DATA; Schema: public; Owner: postgres
--

COPY public.auth_sessions (id, user_id, token, expires_at, created_at) FROM stdin;
session-123	test-user-123	token-abc-123-xyz	2025-12-17 19:10:17.232948	2025-12-17 18:10:17.232948
\.


--
-- Data for Name: franchise_outlets; Type: TABLE DATA; Schema: public; Owner: postgres
--

COPY public.franchise_outlets (id, franchise_id, address, city, state, created_at) FROM stdin;
\.


--
-- Data for Name: franchises; Type: TABLE DATA; Schema: public; Owner: postgres
--

COPY public.franchises (id, name, description, investment_min, investment_max, category, created_at, updated_at) FROM stdin;
test-franchise-001	Test Franchise	A test franchise	50000	150000	food	2025-12-17 18:10:14.940773	2025-12-17 18:10:14.940773
mcdonalds	McDonald's	Fast food giant	1000000	2200000	food	2025-12-17 18:10:15.311008	2025-12-17 18:10:15.311008
subway	Subway	Sandwich chain	150000	300000	food	2025-12-17 18:10:15.384713	2025-12-17 18:10:15.384713
\.


--
-- Data for Name: franchisors; Type: TABLE DATA; Schema: public; Owner: postgres
--

COPY public.franchisors (id, franchise_id, account_type, email, phone, created_at) FROM stdin;
1	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-17 18:10:15.552042
2	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-17 19:29:47.569245
3	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-17 20:18:59.529896
4	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 04:43:18.071393
6	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 08:35:42.692287
5	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 08:35:42.691308
7	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 09:16:56.460636
8	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 10:51:35.148855
9	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 10:51:35.200302
11	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 11:10:14.156436
10	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 11:10:14.137285
12	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 11:41:23.338101
13	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 11:49:04.371858
14	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 12:04:08.666237
15	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 12:08:00.58027
16	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 12:09:52.894985
17	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 12:12:15.097698
18	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 12:15:31.41296
19	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 12:19:15.212038
20	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 12:24:00.658659
21	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 14:24:30.063639
22	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 14:25:55.32785
23	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 14:28:05.524472
24	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 14:45:53.952084
25	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 14:45:55.216432
26	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 15:22:12.647545
27	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 15:22:12.722312
28	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 15:26:01.968225
29	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 15:26:02.953257
30	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 15:36:42.347911
31	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 15:42:20.614011
33	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 17:53:12.62256
32	test-franchise-001	premium	franchisor@test.com	+1234567890	2025-12-18 17:53:12.621273
\.


--
-- Data for Name: user_subscriptions; Type: TABLE DATA; Schema: public; Owner: postgres
--

COPY public.user_subscriptions (id, user_id, tier, expires_at, is_valid, created_at) FROM stdin;
1	test-user-123	premium	2026-12-17 18:10:16.648861	t	2025-12-17 18:10:16.648861
2	user-mcd-456	premium	2026-12-17 18:10:16.938203	t	2025-12-17 18:10:16.938203
\.


--
-- Data for Name: users; Type: TABLE DATA; Schema: public; Owner: postgres
--

COPY public.users (id, email, phone, capital_available, location_preferences, interests, industry_experience, created_at) FROM stdin;
test-user-123	testuser@example.com	+1234567890	100000	["New York"]	["food"]	5	2025-12-17 18:10:16.113394
user-mcd-456	mcduser@example.com	+9876543210	1500000	["Texas", "California"]	["food", "fast_food"]	10	2025-12-17 18:10:16.354892
\.


--
-- Data for Name: zoho_contacts; Type: TABLE DATA; Schema: public; Owner: postgres
--

COPY public.zoho_contacts (id, email, first_name, last_name, phone, company, lead_source, created_at) FROM stdin;
zoho-test-123	zoho@example.com	Test	User	+1234567890	Test Corp	Website	2025-12-17 18:10:17.106938
\.


--
-- Name: audit_log_id_seq; Type: SEQUENCE SET; Schema: public; Owner: postgres
--

SELECT pg_catalog.setval('public.audit_log_id_seq', 30, true);


--
-- Name: franchise_outlets_id_seq; Type: SEQUENCE SET; Schema: public; Owner: postgres
--

SELECT pg_catalog.setval('public.franchise_outlets_id_seq', 1, false);


--
-- Name: franchisors_id_seq; Type: SEQUENCE SET; Schema: public; Owner: postgres
--

SELECT pg_catalog.setval('public.franchisors_id_seq', 33, true);


--
-- Name: user_subscriptions_id_seq; Type: SEQUENCE SET; Schema: public; Owner: postgres
--

SELECT pg_catalog.setval('public.user_subscriptions_id_seq', 66, true);


--
-- Name: applications applications_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.applications
    ADD CONSTRAINT applications_pkey PRIMARY KEY (id);


--
-- Name: applications applications_seeker_id_franchise_id_key; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.applications
    ADD CONSTRAINT applications_seeker_id_franchise_id_key UNIQUE (seeker_id, franchise_id);


--
-- Name: audit_log audit_log_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.audit_log
    ADD CONSTRAINT audit_log_pkey PRIMARY KEY (id);


--
-- Name: auth_sessions auth_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.auth_sessions
    ADD CONSTRAINT auth_sessions_pkey PRIMARY KEY (id);


--
-- Name: auth_sessions auth_sessions_token_key; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.auth_sessions
    ADD CONSTRAINT auth_sessions_token_key UNIQUE (token);


--
-- Name: franchise_outlets franchise_outlets_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.franchise_outlets
    ADD CONSTRAINT franchise_outlets_pkey PRIMARY KEY (id);


--
-- Name: franchises franchises_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.franchises
    ADD CONSTRAINT franchises_pkey PRIMARY KEY (id);


--
-- Name: franchisors franchisors_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.franchisors
    ADD CONSTRAINT franchisors_pkey PRIMARY KEY (id);


--
-- Name: user_subscriptions user_subscriptions_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.user_subscriptions
    ADD CONSTRAINT user_subscriptions_pkey PRIMARY KEY (id);


--
-- Name: user_subscriptions user_subscriptions_user_id_key; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.user_subscriptions
    ADD CONSTRAINT user_subscriptions_user_id_key UNIQUE (user_id);


--
-- Name: users users_email_key; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_email_key UNIQUE (email);


--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);


--
-- Name: zoho_contacts zoho_contacts_email_key; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.zoho_contacts
    ADD CONSTRAINT zoho_contacts_email_key UNIQUE (email);


--
-- Name: zoho_contacts zoho_contacts_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.zoho_contacts
    ADD CONSTRAINT zoho_contacts_pkey PRIMARY KEY (id);


--
-- Name: franchise_outlets franchise_outlets_franchise_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.franchise_outlets
    ADD CONSTRAINT franchise_outlets_franchise_id_fkey FOREIGN KEY (franchise_id) REFERENCES public.franchises(id);


--
-- Name: franchisors franchisors_franchise_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.franchisors
    ADD CONSTRAINT franchisors_franchise_id_fkey FOREIGN KEY (franchise_id) REFERENCES public.franchises(id);


--
-- PostgreSQL database dump complete
--

\unrestrict PepclzoHeiuspAmEthEDSbUhHYQNGKzBCl2cCdeid9UgXXvTifUDZrnFMeTkXoo

