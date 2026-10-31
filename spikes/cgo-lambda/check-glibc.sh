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

newest=$(readelf -V -W "$bin" | grep -o 'GLIBC_[0-9][0-9.]*' | sed 's/^GLIBC_//' | sort -uV | tail -1)
if [[ $(printf '%s\n%s\n' "$newest" "$max" | sort -V | tail -1) != "$max" ]]; then
  echo "FAIL $bin: needs GLIBC_$newest, newer than $max. Symbols:"
  objdump -T "$bin" | grep "GLIBC_$newest" | awk '{print "       " $NF}' | sort -u
  fail=1
fi

if (( fail == 0 )); then
  echo "PASS $bin: needs at most GLIBC_$newest (limit $max); links only glibc"
fi
exit "$fail"
