# AGENTS.md — hoarddb-bson

A Go library implementing BSON as specified at bsonspec.org. Notes for
contributors and coding agents working in this repository.

## Encoded bytes are a contract

A change to any encoded byte sequence needs a new vector in `testdata/vectors/`
and a minor version. A vector is never edited to make a build pass: a vector
change is a failed build, not a fix. Files under `testdata/vectors/`,
`scripts/lint-tool.sh` and `.server-go-version` are hash-checked by
`testdata/vectors/SHA256SUMS`; do not edit them here.

## Licence and marks

1. `LICENSE` is Apache-2.0. Code written here carries the hoardDB Apache-2.0
   header (see `internal/repocheck`).
2. A ported file keeps the upstream header lines intact, adds a
   `Modified by hoardDB, <date>: <summary>` line, and is listed in
   `docs/ported-files.md`.
3. `THIRD-PARTY-NOTICES` and `NOTICE` ship with every copy.
4. No third-party product mark appears in any name: no package path,
   repository, module path, file name or exported identifier
   (`internal/repocheck` enforces it).
5. The README states what the library is and makes no compatibility claim. No
   logo, no registered or trademark sign.

## Checks

From the repository root, with `GOWORK=off`:

    go vet ./... && (cd oracle && go vet ./...)
    go test -race ./... && (cd oracle && go test -race ./...)
    go test -run 'Allocs' ./... && (cd oracle && go test -run 'Allocs' ./...)   # no -race
    bash scripts/check-synced.sh

`oracle/decode_corpus_test.go` skips unless `HOARDDB_V1CORPUS` points at a
corpus file; every other oracle test needs only a Go toolchain.

## Tags

Tags never move. A re-pushed tag changes the module hash and breaks every
`go.sum` that recorded it. A wrong tagged commit is fixed by the next tag.

## Git

Stage by explicit path. Never `git add -A`, `git add .` or a directory.
