# update-offline-data — refresh the embedded public datasets

`update-offline-data` keeps the reference tables that the offline field tools
read in step with the live public catalogs they come from, and tells you exactly
what changed when they drift.

```console
$ ./scripts/update-offline-data.sh -n     # check: report, write nothing
$ ./scripts/update-offline-data.sh        # refresh the tables
$ ./scripts/update-offline-data.sh -n -v  # check, and print each source's fingerprint
```

---

## The data model: complete catalogs, not a selection

Three of the four datasets are carried **in full**:

| Dataset | Source | Filter | File |
|---|---|---|---|
| `buoy` | NOAA NDBC `activestations.xml` | stations with `met="y"` | `bot/buoy-stations.go` |
| `tide` | NOAA CO-OPS tide-prediction stations | stations with an id | `bot/tide-stations.go` |
| `metar` | OurAirports `airports.csv` | an ICAO ident, an IATA code, scheduled service, a name, a city, and a country | `bot/metar-stations.go` |
| `wmm` | NOAA NCEI World Magnetic Model notice | — (checked, never rewritten) | `bot/declination.go` |

A station is in a table because it meets that filter — **not** because somebody
judged it useful. That is what makes `near` answer with the genuinely nearest
station anywhere on earth, and it is why a table is roughly an order of magnitude
larger than a shortlist would be (~0.7 MB of embedded strings across the three;
the growth is linear in the number of stations).

Adding or dropping a station is a change to the filter, never a hand edit to a
generated file. The only table in the package that is not taken from a public
feed is [`tower`](gorrcbot.md), which this tool does not touch.

> [!IMPORTANT]
> **These tables are the providers' catalogs in full.** The field tools sort by
> distance, rank matches by relevance, and paginate, so that carrying every
> station costs the operator nothing but page turns.

---

## What a check reports

```
[UP TO DATE] bot/buoy-stations.go (897 entries)
[UPDATE AVAILABLE] bot/tide-stations.go (upstream: 3501 tide stations) [2 added (9455920 9455921), 3 updated (9414290 ...)]
```

A verdict is decided by comparing the regenerated table with the file, and the
bracketed summary says **what** changed in the terms an operator can act on:
stations added, removed, and updated (a row whose own fields changed), or
`same rows in a different order` when nothing about the data changed at all.

---

## Why a check is idempotent

* The tables are a pure function of the upstream catalog. Rows are ordered by a
  total order (id, then every remaining field) and de-duplicated by key, so a
  feed that merely reorders itself, or lists one station twice, regenerates
  byte-identical output.
* A write goes through a temporary file and a rename, so an interrupted run
  leaves either the old table or the new one — never a truncated file that would
  break the build and make every later check report an update forever.
* A run that writes reports `[UP TO DATE]` on the next check. If a check keeps
  reporting a change, upstream really did change, and the summary names the
  stations.

### Retries and download integrity

A fetch is retried up to four times with an exponential backoff (500 ms doubling
to an 8 s cap) for the failures that can be transient: a transport error, a `5xx`,
or a `429`. A permanent `4xx` is reported once and not retried.

The whole document is read and checked against the `Content-Length` the server
declared before anything is written. A catalog cut short on a line boundary
otherwise parses cleanly and looks exactly like upstream dropping thousands of
stations — the one failure an embedded table must never be rewritten from.

Retries cannot make a healthy source agree with a file that holds a different
selection: a successful fetch is returned on its first attempt, byte for byte.

---

## Options

| Flag | Meaning |
|---|---|
| `-n`, `--dry-run` | Check upstream and report what would change, writing nothing |
| `-v`, `--verbose` | Print each fetch's status, byte count, and SHA-256, plus the per-field drift of each table |
| `--target TARGET` | Limit to one dataset: `all` (default), `buoy`, `tide`, `metar`, `wmm` |
| `--version` | Print the version and exit |
| `-h`, `--help` | Print the usage text and exit |

Use `-n -v` to answer "is the source flaky?" for yourself: two runs that print
the same `sha256` are reading the same catalog, whatever the verdict says.

## See also

- [gorrcbot](gorrcbot.md) — the commands that read these catalogs
- [grl](grl.md) — the same engine as a standalone appliance
