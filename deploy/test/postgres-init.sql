-- Isolated test cluster: application permissions match the production role.
\getenv hllm_password POSTGRES_PASSWORD
CREATE ROLE harden_llm LOGIN PASSWORD :'hllm_password'
  NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
CREATE DATABASE harden_llm OWNER harden_llm;
REVOKE ALL ON DATABASE harden_llm FROM PUBLIC;
\connect harden_llm
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT USAGE, CREATE ON SCHEMA public TO harden_llm;
