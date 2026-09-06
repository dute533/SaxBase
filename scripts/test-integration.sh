#!/usr/bin/env bash
# Run the same disposable SQL Server integration test locally and in CI.
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
cid_file="$temp_dir/container.cid"

cleanup() {
  local status=$? container_id=''
  trap - EXIT INT TERM
  if [[ -s "$cid_file" ]]; then
    container_id="$(cat "$cid_file")"
    if [[ "$container_id" =~ ^[a-f0-9]{64}$ ]]; then
      if (( status != 0 )); then
        echo 'Integration test failed; SQL Server logs follow:' >&2
        docker logs --tail 100 "$container_id" >&2 || true
      fi
      if ! docker rm --force "$container_id" >/dev/null; then
        echo "Could not remove test container $container_id" >&2
        if (( status == 0 )); then status=1; fi
      fi
    fi
  fi
  rm -rf -- "$temp_dir"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

image="${SAXBASE_SQLSERVER_IMAGE:-mcr.microsoft.com/mssql/server:2022-latest}"
echo "Starting disposable SQL Server ($image)..."
# Fixed credentials belong only to this temporary, loopback-bound test server.
docker run --detach --cidfile "$cid_file" \
  --label com.saxbase.integration=true \
  --env ACCEPT_EULA=Y --env MSSQL_PID=Developer \
  --env 'MSSQL_SA_PASSWORD=SaxBase_Test_Only_42!' \
  --publish 127.0.0.1::1433 "$image" >/dev/null

container_id="$(cat "$cid_file")"
address="$(docker port "$container_id" 1433/tcp)"
port="${address##*:}"
[[ "$port" =~ ^[0-9]+$ ]] || { echo "Unexpected published port: $address" >&2; exit 1; }

# The Go test waits for readiness, creates a unique database, applies migrations
# and objects, verifies planning/releases/rollback, then drops its database.
SAXBASE_TEST_SQLSERVER_DSN="sqlserver://sa:SaxBase_Test_Only_42!@127.0.0.1:$port?database=master&encrypt=disable" \
  go test -tags=integration -count=1 -timeout=6m -v ./test/integration
