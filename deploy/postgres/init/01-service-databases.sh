#!/bin/sh
set -eu

psql --set ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
  --set transfer_service_password="$TRANSFER_SERVICE_DB_PASSWORD" \
  --set bank_a_password="$BANK_A_DB_PASSWORD" \
  --set bank_b_password="$BANK_B_DB_PASSWORD" <<'SQL'
CREATE ROLE transfer_service LOGIN PASSWORD :'transfer_service_password';
CREATE DATABASE transfer_service OWNER transfer_service;
REVOKE ALL ON DATABASE transfer_service FROM PUBLIC;

CREATE ROLE bank_a LOGIN PASSWORD :'bank_a_password';
CREATE DATABASE bank_a OWNER bank_a;
REVOKE ALL ON DATABASE bank_a FROM PUBLIC;

CREATE ROLE bank_b LOGIN PASSWORD :'bank_b_password';
CREATE DATABASE bank_b OWNER bank_b;
REVOKE ALL ON DATABASE bank_b FROM PUBLIC;
SQL
