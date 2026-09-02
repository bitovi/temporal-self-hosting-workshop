#! /bin/bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Forces every pod in the cluster to restart fresh, then runs start.sh's full
# ensure/verify pass against the result. Just re-running start.sh on its own
# isn't enough for this: against an already-healthy cluster, most of its
# ensure_* functions no-op (they only act on a missing/unhealthy release), so
# nothing actually cycles. Stopping the k3d node containers first forces a
# real restart of every pod when they come back, not just a health recheck.
#
# Safe for data: Postgres, MinIO, and Prometheus all persist to PVCs on the
# `local-path` storage class (confirmed live via `kubectl get storageclass`/
# `get pvc` against this repo's own cluster), which stores each PV as a
# directory on the k3d node container's own filesystem. `k3d cluster stop`/
# `start` only pauses/resumes that container -- it never touches its
# filesystem -- so workflow history and archived data both survive a
# restart. Only `k3d cluster delete dev` would lose them.
if k3d cluster list | grep -q '^dev\s'; then
  echo "Stopping k3d cluster 'dev' to cycle every pod..."
  k3d cluster stop dev
else
  echo "k3d cluster 'dev' doesn't exist yet -- nothing to cycle, starting fresh."
fi

echo "Bringing the environment back up..."
exec _scripts/start.sh
