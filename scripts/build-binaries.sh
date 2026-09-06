#!/usr/bin/env bash
set -euo pipefail

# Run from the repository root. Each archive includes setup templates and docs.
label=${1:-snapshot}
if [[ ! "$label" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
  echo 'Invalid build label' >&2
  exit 1
fi
mkdir -p dist
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os=${platform%/*}
  arch=${platform#*/}
  name="saxbase_${label}_${os}_${arch}"
  package="$stage/$name"
  mkdir -p "$package/docs"
  binary=saxbase
  if [[ "$os" == windows ]]; then binary=saxbase.exe; fi
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "-X saxbase/internal/cli.Version=$label" -o "$package/$binary" .
  cp LICENSE README.md saxbase.yaml.example .env.example "$package/"
  cp docs/reference.md "$package/docs/"
  cp -R examples "$package/"
  if [[ "$os" == windows ]]; then
    python3 - "$stage" "$name" <<'PYZIP'
import pathlib
import sys
import zipfile
stage, name = pathlib.Path(sys.argv[1]), sys.argv[2]
with zipfile.ZipFile(pathlib.Path("dist") / (name + ".zip"), "w", zipfile.ZIP_DEFLATED) as archive:
    for path in sorted((stage / name).rglob("*")):
        if path.is_file():
            archive.write(path, path.relative_to(stage))
PYZIP
  else
    tar -czf "dist/$name.tar.gz" -C "$stage" "$name"
  fi
done
(cd dist && sha256sum "saxbase_${label}_"*.tar.gz "saxbase_${label}_"*.zip > "saxbase_${label}_checksums.txt")
