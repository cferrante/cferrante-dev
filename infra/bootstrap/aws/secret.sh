# shellcheck shell=bash
# put_secure_string NAME VALUE DESCRIPTION : stores VALUE in SSM as a
# SecureString through the `project` CLI function, overwriting any old value.
# VALUE and DESCRIPTION must need no JSON escaping. Sourced by lib.sh.
#
# The value reaches the CLI in a JSON file, never on its command line, where
# any process could read it. The file must be a real one: AWS CLI 2.37.5
# reads nothing from a pipe (file:///dev/stdin, a here-string, <(...)) and
# reports "Invalid JSON received". So it is mode 600, in a private directory
# on tmpfs (memory, never disk), and deleted as soon as the call returns, or
# on exit if the script is interrupted.
put_secure_string() {
  local base fs rc=0
  base=${XDG_RUNTIME_DIR:-/dev/shm}
  fs=$(stat -f -c %T "$base" 2>/dev/null) || fs=""
  [[ $fs == tmpfs ]] || die "$base isn't tmpfs; not writing a secret to it"
  secret_dir=$(umask 077 && mktemp -d -p "$base")
  trap 'rm -rf "$secret_dir"' EXIT
  # printf is a shell builtin, so VALUE isn't on any process's command line.
  (umask 077 && printf \
    '{"Name":"%s","Type":"SecureString","Tier":"Standard","Overwrite":true,"Value":"%s","Description":"%s"}' \
    "$1" "$2" "$3" >"$secret_dir/param.json")
  project ssm put-parameter --cli-input-json "file://$secret_dir/param.json" >/dev/null || rc=$?
  rm -rf "$secret_dir"
  return "$rc"
}
