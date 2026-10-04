#!/usr/bin/env bash
# Check payload.yaml against grid.yaml and, unless OFFLINE=1, against GitHub.
set -euo pipefail
cd "$(dirname "$0")"
p=payload.yaml
status=0
fail() { echo "payload: $*"; status=1; }

for img in operator enrollment gateway; do
	want=$(yq -r ".images.$img" $p)
	have=$(yq -r ".artifact.images.$img.digest" grid.yaml)
	if [ "$want" = pending ]; then
		echo "payload: $img digest pending"
	elif [ "$want" != "$have" ]; then
		fail "$img digest in grid.yaml is $have, payload.yaml has $want"
	fi
done

if [ "${OFFLINE:-0}" = 1 ]; then
	echo "payload: OFFLINE=1, skipping GitHub checks"
	exit $status
fi

repo=$(yq -r .repo $p)
base=$(yq -r .base $p)
sha=${base#*@}
ref=${base%@*}
cmp=$(gh api "repos/$repo/compare/$sha...$ref" --jq .status)
case $cmp in
	ahead | identical) ;;
	*) fail "base $sha is not an ancestor of $ref ($cmp)" ;;
esac

for entry in $(yq -r '.prs[]' $p); do
	n=${entry%@*}
	head=${entry#*@}
	read -r state merged actual < <(gh api "repos/$repo/pulls/$n" --jq '"\(.state) \(.merged) \(.head.sha)"')
	if [ "$state" = closed ] && [ "$merged" != true ]; then
		fail "#$n is closed without merging"
	fi
	if [ "$head" = pending ]; then
		echo "payload: #$n head pending (GitHub has ${actual:0:8})"
	elif [[ $actual != "$head"* ]]; then
		fail "#$n head on GitHub is ${actual:0:8}, payload.yaml has $head"
	fi
done
exit $status
