#!/bin/sh
set -eu

PROFILE=${1:-coverage.out}
LIMIT=70

awk -v limit="$LIMIT" '
  NR > 1 {
    split($1, parts, ":")
    n = split(parts[1], path, "/")
    pkg = path[n - 1]
    key = $1
    stmts[key] = $2
    if ($3 > 0) hit[key] = 1
    owner[key] = pkg
  }
  END {
    for (key in stmts) {
      total[owner[key]] += stmts[key]
      if (key in hit) covered[owner[key]] += stmts[key]
    }
    split("pricing allocation booking folio nightaudit channel assign", domain, " ")
    failed = 0
    for (i = 1; i <= 7; i++) {
      pkg = domain[i]
      pct = total[pkg] ? covered[pkg] * 100 / total[pkg] : 0
      status = pct >= limit ? "PASS" : "FAIL"
      if (pct < limit) failed = 1
      printf "%-12s %6.1f%%  %s\n", pkg, pct, status
      all += total[pkg]
      done += covered[pkg]
    }
    printf "%-12s %6.1f%%\n", "alan toplamı", done * 100 / all
    exit failed
  }
' "$PROFILE"
