# Reachability gate for govulncheck's text output.
#
# govulncheck prints CALLED vulnerabilities under "=== Symbol Results ===" -
# meaning it traced a path from this module's own code to the vulnerable symbol.
# Everything below that section is "you import it / you require it", which is
# not the same claim and is not what this gate acts on.
#
# The rule: fail on a reachable vulnerability that HAS a fix. Some do not.
# github.com/docker/docker's advisories are daemon-side and v28.5.2+incompatible
# is the newest version its Go module has, so there is nothing to move to.
# Failing on those would paint this gate red permanently, and a gate that is
# always red is one somebody deletes. They are printed instead, so they stay
# visible without blocking - and the day upstream publishes a fix, this turns
# red on its own without anyone editing a list.

/^=== Symbol Results ===/ { sym = 1; next }
/^=== /                   { sym = 0 }

sym && /^Vulnerability #/ { id = $3 }

sym && /^    Fixed in:/ {
  fix = $0
  sub(/^    Fixed in: /, "", fix)
  if (fix == "N/A") {
    print "  no fix available upstream: " id
    unfixable++
  } else {
    print "  FIXABLE: " id " -> " fix
    fixable++
  }
}

END {
  if (fixable > 0) {
    print ""
    print fixable " reachable vulnerability(ies) have a fix available."
    print "Update the dependency (or the Go toolchain, for a stdlib entry) and re-run."
    exit 1
  }
  if (unfixable > 0) {
    print ""
    print unfixable " reachable vulnerability(ies) have no fix upstream. Not blocking."
  } else {
    print "  nothing reachable"
  }
}
