#!/usr/bin/env bash
# End-to-end smoke test for the raffle server. Starts the server, exercises
# the HTTP API, then stops the server. All output is captured.
set -uo pipefail

cd /home/lucas/Documents/Projects/GoRaffle || exit 1
LOG=/tmp/opencode/raffle_e2e.log
rm -f "$LOG"

go build -o raffle ./cmd/raffle || exit 1

# Dedicated port: never collide with a deployed instance (e.g. the docker
# compose app on 8543), or this script would silently test *that* server's
# code instead of the binary built above.
PORT=8599

DATABASE_URL="postgresql://user:pass@localhost:5432/raffle?sslmode=disable" \
  LISTEN_ADDR=":$PORT" \
  setsid ./raffle >"$LOG" 2>&1 </dev/null &
SRV=$!

# wait for server
for i in $(seq 1 20); do
  code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$PORT/" 2>/dev/null || true)
  [ "$code" = "200" ] && break
  sleep 0.5
done
if [ "$code" != "200" ]; then
  echo "FAIL: server did not come up on port $PORT"
  cat "$LOG"
  exit 1
fi

base=http://localhost:$PORT
pass=0; fail=0
check() { # check <desc> <expected> <actual>
  if [ "$2" = "$3" ]; then pass=$((pass+1)); echo "PASS: $1";
  else fail=$((fail+1)); echo "FAIL: $1 (expected [$2], got [$3])"; fi
}

# Start from a clean database state.
echo "=== reset database for a clean run ==="
curl -s -X POST "$base/api/reset" >/dev/null
curl -s -X DELETE "$base/api/entries" >/dev/null
curl -s -X POST "$base/api/import" -H 'Content-Type: application/json' \
  -d '{"entries":[{"name":"entry 1"},{"name":"entry 2"},{"name":"entry 3"},{"name":"entry 4"},{"name":"entry 5"},{"name":"entry 6"},{"name":"entry 7"},{"name":"entry 8"},{"name":"entry 9"},{"name":"entry 10"},{"name":"entry 11"},{"name":"entry 12"}]}' >/dev/null

echo "=== page ==="
html=$(curl -s "$base/")
contains() { case "$html" in *"$1"*) echo "YES";; *) echo "NO";; esac; }
check "index loads" 200 "$(curl -s -o /dev/null -w "%{http_code}" "$base/")"
[ "$(contains 'Welcome to the draw!')" = "YES" ] && { pass=$((pass+1)); echo "PASS: welcome text"; } || { fail=$((fail+1)); echo "FAIL: welcome text"; }
[ "$(contains 'id="entry-table"')" = "YES" ] && { pass=$((pass+1)); echo "PASS: table section"; } || { fail=$((fail+1)); echo "FAIL: table section"; }
[ "$(contains 'id="filter-bar"')" = "YES" ] && { pass=$((pass+1)); echo "PASS: filter bar"; } || { fail=$((fail+1)); echo "FAIL: filter bar"; }
[ "$(contains 'id="history"')" = "YES" ] && { pass=$((pass+1)); echo "PASS: history"; } || { fail=$((fail+1)); echo "FAIL: history"; }

echo "=== entries ==="
cs=$(curl -s "$base/api/entries")
check "seeded 12 entries" "12" "$(printf '%s' "$cs" | python3 -c 'import sys,json;print(len(json.load(sys.stdin)))' 2>/dev/null || echo ERR)"

add=$(curl -s -X POST "$base/api/entries" -H 'Content-Type: application/json' -d '{"name":"Alice"}')
ALICE_ID=$(printf '%s' "$add" | python3 -c 'import sys,json;d=json.load(sys.stdin);print(d["id"])' 2>/dev/null)
check "add Alice returned a name" "Alice" "$(printf '%s' "$add" | python3 -c 'import sys,json;print(json.load(sys.stdin)["name"])' 2>/dev/null || echo ERR)"

dup=$(curl -s -X POST "$base/api/entries" -H 'Content-Type: application/json' -d '{"name":"alice"}')
check "duplicate is no-op (same id)" "$ALICE_ID" "$(printf '%s' "$dup" | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])' 2>/dev/null || echo ERR)"

echo "=== tags ==="
t1=$(curl -s -X POST "$base/api/entries/$ALICE_ID/tags" -H 'Content-Type: application/json' -d '{"name":"VIP"}')
tg=$(curl -s -X POST "$base/api/entries/$ALICE_ID/tags" -H 'Content-Type: application/json' -d '{"name":"TeamA"}')
check "add two tags" "2" "$(printf '%s' "$tg" | python3 -c 'import sys,json;print(len(json.load(sys.stdin)))' 2>/dev/null || echo ERR)"
tl=$(curl -s "$base/api/entries/$ALICE_ID/tags")
check "list tags for alice" "2" "$(printf '%s' "$tl" | python3 -c 'import sys,json;print(len(json.load(sys.stdin)))' 2>/dev/null || echo ERR)"
allt=$(curl -s "$base/api/tags")
alltags=$(printf '%s' "$allt" | python3 -c 'import sys,json;print("|".join(sorted(t["name"] for t in json.load(sys.stdin))))' 2>/dev/null)
check "all tags ever used includes VIP" "yes" "$(printf '%s' "$alltags" | grep -q VIP && echo yes || echo no)"
check "all tags ever used includes TeamA" "yes" "$(printf '%s' "$alltags" | grep -q TeamA && echo yes || echo no)"

echo "=== filter=="
# Give Alice a high count so she is NOT min-count, then tag-filter to the VIP pool.
curl -s -X PUT "$base/api/entries/$ALICE_ID" -H 'Content-Type: application/json' -d '{"pick_count":5}' >/dev/null
curl -s -X PUT "$base/api/entries/2" -H 'Content-Type: application/json' -d '{"excluded":true}' >/dev/null

echo "=== draw (filter must have VIP, any_of empty) ==="
draw=$(curl -s -X POST "$base/api/draw" -H 'Content-Type: application/json' -d '{"must_have":["VIP"],"any_of":[]}')
check "draw returns winner name" "Alice" "$(printf '%s' "$draw" | python3 -c 'import sys,json;print(json.load(sys.stdin)["winner"]["name"])' 2>/dev/null || echo ERR)"

echo "=== undo ==="
undo=$(curl -s -X POST "$base/api/undo")
check "undo message" "Removed last entry: Alice" "$(printf '%s' "$undo" | python3 -c 'import sys,json;print(json.load(sys.stdin)["info"])' 2>/dev/null || echo ERR)"

echo "=== draw all-excluded/empty pool ==="
curl -s -X PUT "$base/api/entries/$ALICE_ID" -H 'Content-Type: application/json' -d '{"excluded":true}' >/dev/null
err=$(curl -s -X POST "$base/api/draw" -H 'Content-Type: application/json' -d '{"must_have":["VIP"],"any_of":[]}')
check "no eligible error" "no eligible entries" "$(printf '%s' "$err" | python3 -c 'import sys,json;print(json.load(sys.stdin)["error"])' 2>/dev/null || echo ERR)"
curl -s -X PUT "$base/api/entries/$ALICE_ID" -H 'Content-Type: application/json' -d '{"excluded":false}' >/dev/null

echo "=== history ==="
hist=$(curl -s "$base/api/history")
check "history empty" "0" "$(printf '%s' "$hist" | python3 -c 'import sys,json;print(len(json.load(sys.stdin)))' 2>/dev/null || echo ERR)"

echo "=== list name (setup) ==="
# The list name is user state; snapshot it and normalize to the default so the
# export checks below are deterministic, then restore it at the end.
orig_ln=$(curl -s "$base/api/list-name" | python3 -c 'import sys,json;print(json.load(sys.stdin)["name"])' 2>/dev/null || echo Entries)
curl -s -X PUT "$base/api/list-name" -H 'Content-Type: application/json' -d '{"name":"Entries"}' >/dev/null

echo "=== export/import ==="
export_json=$(curl -s "$base/api/export?format=json")
check "export json has entries" "13" "$(printf '%s' "$export_json" | python3 -c 'import sys,json;print(len(json.load(sys.stdin)["entries"]))' 2>/dev/null || echo ERR)"
check "export json has list_name" "Entries" "$(printf '%s' "$export_json" | python3 -c 'import sys,json;print(json.load(sys.stdin)["list_name"])' 2>/dev/null || echo ERR)"
curl -s -X POST "$base/api/import" -H 'Content-Type: application/json' -d '{"entries":[{"name":"Bob","pick_count":2,"excluded":false,"tags":["Newbie"]},{"name":"Carol"}]}' >/dev/null
cs2=$(curl -s "$base/api/entries")
check "import added 2 entries (15 total)" "15" "$(printf '%s' "$cs2" | python3 -c 'import sys,json;print(len(json.load(sys.stdin)))' 2>/dev/null || echo ERR)"
bob=$(printf '%s' "$cs2" | python3 -c 'import sys,json;print(next(c["pick_count"] for c in json.load(sys.stdin) if c["name"]=="Bob"))' 2>/dev/null)
check "import set Bob pick_count" "2" "$bob"
bobtags=$(printf '%s' "$cs2" | python3 -c 'import sys,json;print(",".join(next(c["tags"] for c in json.load(sys.stdin) if c["name"]=="Bob")))' 2>/dev/null)
check "import set Bob tags" "Newbie" "$bobtags"

echo "=== CSV export ==="
csv=$(curl -s "$base/api/export?format=csv")
check "csv first row is list_name" "list_name" "$(printf '%s' "$csv" | head -1 | cut -d, -f1)"
check "csv second row is column header" "name" "$(printf '%s' "$csv" | sed -n 2p | cut -d, -f1)"

echo "=== list name ==="
curl -s -X PUT "$base/api/list-name" -H 'Content-Type: application/json' -d '{"name":"My List"}' >/dev/null
ln2=$(curl -s "$base/api/list-name")
check "updated list name" "My List" "$(printf '%s' "$ln2" | python3 -c 'import sys,json;print(json.load(sys.stdin)["name"])' 2>/dev/null || echo ERR)"
fn=$(curl -s -D - -o /dev/null "$base/api/export?format=json&name=My%20List" | grep -i '^content-disposition' | sed 's/.*filename="\([^"]*\)".*/\1/' | tr -d '\r')
check "export filename follows list name" "My_List.json" "$fn"
imp=$(curl -s -X POST "$base/api/import" -H 'Content-Type: application/json' -d '{"list_name":"Swapped","entries":[{"name":"ListNameTester"}]}')
check "import response carries list_name" "Swapped" "$(printf '%s' "$imp" | python3 -c 'import sys,json;print(json.load(sys.stdin)["list_name"])' 2>/dev/null || echo ERR)"
ln3=$(curl -s "$base/api/list-name")
check "import set list name" "Swapped" "$(printf '%s' "$ln3" | python3 -c 'import sys,json;print(json.load(sys.stdin)["name"])' 2>/dev/null || echo ERR)"
csvname=$(curl -s "$base/api/export?format=csv" | head -1 | cut -d, -f2)
check "csv carries list name" "Swapped" "$csvname"

echo "=== CSV round-trip ==="
# Export the current state, clear, re-import: the counts must survive and no
# bogus "list_name" entry may appear from the header rows.
curl -s "$base/api/export?format=csv" > /tmp/opencode/raffle_rt.csv
before=$(curl -s "$base/api/entries" | python3 -c 'import sys,json;print(len(json.load(sys.stdin)))')
curl -s -X DELETE "$base/api/entries" >/dev/null
curl -s -X POST "$base/api/import" -F "file=@/tmp/opencode/raffle_rt.csv" >/dev/null
after=$(curl -s "$base/api/entries" | python3 -c 'import sys,json;print(len(json.load(sys.stdin)))')
check "csv round-trip preserves entry count" "$before" "$after"
bogus=$(curl -s "$base/api/entries" | python3 -c 'import sys,json;print("yes" if any(c["name"] in ("list_name","name") for c in json.load(sys.stdin)) else "no")')
check "csv round-trip adds no header entries" "no" "$bogus"
# restore the default so the app starts clean for the user later
curl -s -X PUT "$base/api/list-name" -H 'Content-Type: application/json' -d '{"name":"Entries"}' >/dev/null

echo "=== reset ==="
curl -s -X POST "$base/api/reset" >/dev/null
cs3=$(curl -s "$base/api/entries")
counts=$(printf '%s' "$cs3" | python3 -c 'import sys,json;print(sorted(set(c["pick_count"] for c in json.load(sys.stdin))))' 2>/dev/null)
check "all counts reset to 0" "[0]" "$counts"

echo "=== clear entries ==="
curl -s -X DELETE "$base/api/entries" >/dev/null
cs4=$(curl -s "$base/api/entries")
check "entries cleared" "0" "$(printf '%s' "$cs4" | python3 -c 'import sys,json;print(len(json.load(sys.stdin)))' 2>/dev/null || echo ERR)"

echo "=== partials ==="
check "table partial" 200 "$(curl -s -o /dev/null -w "%{http_code}" "$base/partials/table")"
check "filter partial" 200 "$(curl -s -o /dev/null -w "%{http_code}" "$base/partials/filter")"
check "history partial" 200 "$(curl -s -o /dev/null -w "%{http_code}" "$base/partials/history")"
check "result partial" 200 "$(curl -s -o /dev/null -w "%{http_code}" "$base/partials/result")"
check "import-export partial" 200 "$(curl -s -o /dev/null -w "%{http_code}" "$base/partials/import-export")"
check "static css" 200 "$(curl -s -o /dev/null -w "%{http_code}" "$base/static/css/style.css")"
check "static js app" 200 "$(curl -s -o /dev/null -w "%{http_code}" "$base/static/js/app.js")"
check "static js ws" 200 "$(curl -s -o /dev/null -w "%{http_code}" "$base/static/js/ws.js")"

# re-seed defaults so the app starts clean for the user later
curl -s -X POST "$base/api/import" -H 'Content-Type: application/json' -d '{"entries":[{"name":"entry 1"},{"name":"entry 2"},{"name":"entry 3"},{"name":"entry 4"},{"name":"entry 5"},{"name":"entry 6"},{"name":"entry 7"},{"name":"entry 8"},{"name":"entry 9"},{"name":"entry 10"},{"name":"entry 11"},{"name":"entry 12"}]}' >/dev/null
# put the user's list name back the way we found it
curl -s -X PUT "$base/api/list-name" -H 'Content-Type: application/json' -d "{\"name\":\"$orig_ln\"}" >/dev/null

kill "$SRV" 2>/dev/null
wait "$SRV" 2>/dev/null

echo
echo "RESULT: $pass passed, $fail failed"
exit $([ "$fail" = 0 ] && echo 0 || echo 1)