// This directory is never actually built as a module: it's staging source
// that the Dockerfile COPYs (only the .go files, not this go.mod) into a
// cloned omes checkout's own scenarios/ package. This go.mod exists purely
// so `go build ./...` from worker-control-ui's root treats this directory
// as a separate module boundary and skips it, since it has no dependency on
// (and isn't part of) worker-control-ui's own module.
module omes-scenarios-staging

go 1.23
