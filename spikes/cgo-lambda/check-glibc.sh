#!/usr/bin/env bash
# Checks that a Linux binary will load on Lambda's Amazon Linux 2023:
#   - it needs no glibc symbol version newer than MAX (default 2.34), and
#   - it links only glibc's own libraries dynamically. A C++ runtime
#     (libstdc++, libgcc_s) built here won't match the one on Lambda.
# A statically linked binary needs nothing from the system and passes.
set -euo pipefail

bin=${1:?usage: check-glibc.sh BINARY [MAX_GLIBC]}
max=${2:-2.34}

needed=$(readelf -d "$bin" | sed -n 's/.*(NEEDED).*\[\(.*\)\]/\1/p')
if [[ -z $needed ]]; then
  echo "PASS $bin: statically linked; needs no system libraries"
  exit 0
fi

fail=0
for lib in $needed; do
  case $lib in
    libc.so.* | libm.so.* | libpthread.so.* | libdl.so.* | librt.so.* | libresolv.so.* | ld-linux*) ;;
    *) echo "FAIL $bin: links $lib dynamically (not part of glibc)"; fail=1 ;;
  esac
done

# newer_than A B: true if version A is newer than version B.
newer_than() { [[ $1 != "$2" && $(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -1) == "$1" ]]; }

newest=$(readelf -V -W "$bin" | grep -o 'GLIBC_[0-9][0-9.]*' | sed 's/^GLIBC_//' | sort -uV | tail -1)
if newer_than "$newest" "$max"; then
  echo "FAIL $bin: needs GLIBC_$newest, newer than $max. Symbols over the limit:"
  # objdump -T lines end in "(GLIBC_x.y) symbol" or "GLIBC_x.y symbol".
  objdump -T "$bin" | sed -n 's/.*[( ]GLIBC_\([0-9.]*\))\{0,1\} \{1,\}\([^ ]*\)$/\1 \2/p' | sort -u -V |
    while read -r version symbol; do
      if newer_than "$version" "$max"; then echo "       $symbol (GLIBC_$version)"; fi
    done
  fail=1
fi

if (( fail == 0 )); then
  echo "PASS $bin: needs at most GLIBC_$newest (limit $max); links only glibc"
fi
exit "$fail"
