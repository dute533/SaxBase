#!/usr/bin/env bash
# Run the disposable SQL Server and PostgreSQL integration tests locally and in CI.
set -Eeuo pipefail

if (( $# != 0 )); then
  echo 'Usage: bash scripts/test-integration.sh' >&2
  exit 2
fi

command -v docker >/dev/null || { echo 'Docker is required.' >&2; exit 1; }
command -v go >/dev/null || { echo 'Go is required.' >&2; exit 1; }
docker info >/dev/null

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/saxbase-integration.XXXXXXXX")"
sqlserver_cid_file="$temp_dir/sqlserver.cid"
postgres_cid_file="$temp_dir/postgres.cid"

cleanup() {
  local status=$? container_id='' label='' cid_file=''
  trap - EXIT INT TERM
  for entry in "SQL Server:$sqlserver_cid_file" "PostgreSQL:$postgres_cid_file"; do
    label="${entry%%:*}"
    cid_file="${entry#*:}"
    if [[ ! -s "$cid_file" ]]; then
      continue
    fi
    container_id="$(cat "$cid_file")"
    if [[ "$container_id" =~ ^[a-f0-9]{64}$ ]]; then
      if (( status != 0 )); then
        echo "Integration test failed; $label logs follow:" >&2
        docker logs --tail 100 "$container_id" >&2 || true
      fi
      if ! docker rm --force "$container_id" >/dev/null; then
        echo "Could not remove test container $container_id" >&2
        if (( status == 0 )); then status=1; fi
      fi
    fi
  done
  rm -rf -- "$temp_dir"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

sqlserver_image="${SAXBASE_SQLSERVER_IMAGE:-mcr.microsoft.com/mssql/server:2022-latest}"
echo "Starting disposable SQL Server ($sqlserver_image)..."
# Fixed credentials belong only to this temporary, loopback-bound test server.
docker run --detach --cidfile "$sqlserver_cid_file" \
  --label com.saxbase.integration=true \
  --env ACCEPT_EULA=Y --env MSSQL_PID=Developer \
  --env 'MSSQL_SA_PASSWORD=SaxBase_Test_Only_42!' \
  --publish 127.0.0.1::1433 "$sqlserver_image" >/dev/null

sqlserver_id="$(cat "$sqlserver_cid_file")"
sqlserver_address="$(docker port "$sqlserver_id" 1433/tcp)"
sqlserver_port="${sqlserver_address##*:}"
[[ "$sqlserver_port" =~ ^[0-9]+$ ]] || { echo "Unexpected SQL Server port: $sqlserver_address" >&2; exit 1; }

postgres_image="${SAXBASE_POSTGRES_IMAGE:-postgres:17-alpine}"
echo "Starting disposable PostgreSQL ($postgres_image)..."
docker run --detach --cidfile "$postgres_cid_file" \
  --label com.saxbase.integration=true \
  --env 'POSTGRES_PASSWORD=SaxBase_Test_Only_42!' \
  --publish 127.0.0.1::5432 "$postgres_image" >/dev/null

postgres_id="$(cat "$postgres_cid_file")"
postgres_address="$(docker port "$postgres_id" 5432/tcp)"
postgres_port="${postgres_address##*:}"
[[ "$postgres_port" =~ ^[0-9]+$ ]] || { echo "Unexpected PostgreSQL port: $postgres_address" >&2; exit 1; }

# The Go test waits for readiness, creates a unique database, applies migrations
# and objects, verifies planning/releases/rollback, then drops its database.
SAXBASE_TEST_SQLSERVER_DSN="sqlserver://sa:SaxBase_Test_Only_42!@127.0.0.1:$sqlserver_port?database=master&encrypt=disable" \
SAXBASE_TEST_POSTGRES_DSN="postgres://postgres:SaxBase_Test_Only_42!@127.0.0.1:$postgres_port/postgres?sslmode=disable" \
  go test -tags=integration -count=1 -timeout=10m -v ./test/integration
