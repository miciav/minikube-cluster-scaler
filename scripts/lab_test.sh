#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
# Canonical path: readlink -f resolves macOS /var to /private/var.
tmp=$(cd "$(mktemp -d)" && pwd -P)
trap 'rm -rf "$tmp"' EXIT
# Only the utilities lab needs: CI runners ship kubectl and docker in /usr/bin.
mkdir "$tmp/sys"
for tool in awk dirname id ls mkdir od readlink tr; do
  ln -s "$(command -v "$tool")" "$tmp/sys/$tool"
done
unset MINIKUBE_HOME KUBECONFIG LAB_IMAGE LAB_MINIKUBE LAB_KUBECTL PROFILE MIN_NODES DOCKER_HOST 2>/dev/null || true

elf() {
  printf '\177ELF fake binary\n' >"$1"
  chmod +x "$1"
}

# setup NAME creates a case directory with fake ELF tools, a docker stub and a socket file.
setup() {
  case_dir="$tmp/$1"
  mkdir -p "$case_dir/bin" "$case_dir/home"
  elf "$case_dir/bin/minikube"
  elf "$case_dir/bin/kubectl"
  cat >"$case_dir/bin/docker" <<'STUB'
#!/bin/sh
printf '%s\n' "$*" >>"$DOCKER_LOG"
STUB
  chmod +x "$case_dir/bin/docker"
  : >"$case_dir/docker.sock"
}

# lab ARGS... runs the wrapper for the current case without a terminal;
# extra NAME=value words in $extra_env are added to its environment.
extra_env=
lab() {
  # shellcheck disable=SC2086 # extra_env is a list of NAME=value words without spaces
  env HOME="$case_dir/home" PATH="$case_dir/bin:$tmp/sys" DOCKER_LOG="$case_dir/docker.log" \
    DOCKER_HOST="unix://$case_dir/docker.sock" $extra_env ./scripts/lab "$@" </dev/null >"$case_dir/out" 2>"$case_dir/err"
}

fails() {
  status=0
  lab "$@" || status=$?
  test "$status" -ne 0
  if [ -f "$case_dir/docker.log" ] && grep -q '^run ' "$case_dir/docker.log"; then exit 1; fi
}

# happy path
setup happy
lab start --foo
argv=$(cat "$case_dir/docker.log")
for expected in 'run --rm --init' '--network host' "--user $(id -u):$(id -g)" \
  "$case_dir/docker.sock:/var/run/docker.sock" "$case_dir/bin/minikube:/usr/local/bin/minikube:ro" \
  "$case_dir/bin/kubectl:/usr/local/bin/kubectl:ro" '-e MINIKUBE_HOME' '-e KUBECONFIG' '__LAB_IMAGE__ start --foo'; do
  printf '%s\n' "$argv" | grep -Fq -- "$expected"
done
extra_env=LAB_IMAGE=custom:1
lab start
extra_env=
grep -Fq 'custom:1 start' "$case_dir/docker.log"

# no tty flags without terminal
setup notty
lab deploy
if grep -Eq ' -(i|t|it) ' "$case_dir/docker.log"; then exit 1; fi

# missing state dirs are created
setup state
test ! -e "$case_dir/home/.minikube"
lab start
test -d "$case_dir/home/.minikube"
test -d "$case_dir/home/.kube"

# symlink resolved
setup symlink
mkdir "$case_dir/real"
elf "$case_dir/real/kubectl-1.35"
rm "$case_dir/bin/kubectl"
ln -s "$case_dir/real/kubectl-1.35" "$case_dir/bin/kubectl"
lab start
grep -Fq "$case_dir/real/kubectl-1.35:/usr/local/bin/kubectl:ro" "$case_dir/docker.log"

# explicit override
setup override
mkdir "$case_dir/other"
elf "$case_dir/other/minikube"
extra_env="LAB_MINIKUBE=$case_dir/other/minikube"
lab start
extra_env=
grep -Fq "$case_dir/other/minikube:/usr/local/bin/minikube:ro" "$case_dir/docker.log"

# script shim rejected
setup shim
printf '#!/bin/sh\nexec snap run minikube "$@"\n' >"$case_dir/bin/minikube"
fails start
grep -q 'minikube' "$case_dir/err"
grep -q 'official binary' "$case_dir/err"

# missing kubectl
setup nokubectl
rm "$case_dir/bin/kubectl"
fails start
grep -q 'kubectl not found' "$case_dir/err"

# unwritable socket (root can always write, so the case only means something for users)
if [ "$(id -u)" -ne 0 ]; then
  setup socket
  chmod 000 "$case_dir/docker.sock"
  fails start
  grep -q 'docker group' "$case_dir/err"
fi

# multi-path KUBECONFIG rejected
setup kubeconfig
extra_env=KUBECONFIG=a:b
fails start
extra_env=
grep -q 'KUBECONFIG' "$case_dir/err"

# forwards only set variables
setup forward
extra_env=PROFILE=x
lab start
extra_env=
grep -Fq -- '-e PROFILE' "$case_dir/docker.log"
if grep -Fq -- '-e MIN_NODES' "$case_dir/docker.log"; then exit 1; fi

# check shows what was discovered
setup check
lab check
grep -Fq "minikube: $case_dir/bin/minikube" "$case_dir/out"
grep -Fq "kubectl: $case_dir/bin/kubectl" "$case_dir/out"
grep -q ' check$' "$case_dir/docker.log"
