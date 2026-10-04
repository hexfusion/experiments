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
built=$(yq -r '.built // "null"' $p)
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
	# built~reviewed: the images hold the first head; the PR was rewritten to the second,
	# reviewed as the same content. Accepted only while GitHub's head is the second.
	reviewed=
	if [[ $head == *~* ]]; then
		reviewed=${head#*~}
		head=${head%~*}
	fi
	if [ "$n" = pending ]; then
		echo "payload: a listed PR is not opened yet"
		continue
	fi
	read -r state merged actual < <(gh api "repos/$repo/pulls/$n" --jq '"\(.state) \(.merged) \(.head.sha)"')
	if [ "$state" = closed ] && [ "$merged" != true ]; then
		fail "#$n is closed without merging"
	fi
	if [ -n "$reviewed" ]; then
		if [[ $actual == "$reviewed"* ]]; then
			echo "payload: #$n built at $head, its head $reviewed reviewed as the same content"
		else
			fail "#$n head on GitHub is ${actual:0:8}, payload.yaml reviewed $reviewed"
		fi
	elif [ "$head" = pending ]; then
		echo "payload: #$n head pending (GitHub has ${actual:0:8})"
	elif [[ $actual != "$head"* ]]; then
		moved=$(gh api "repos/$repo/compare/$head...$actual" --jq .status)
		if [ "$built" != null ] && { [ "$state" = open ] || [ "$merged" = true ]; } && [ "$moved" = ahead ]; then
			echo "payload: #$n moved to ${actual:0:8} after the build at $built"
		else
			fail "#$n head on GitHub is ${actual:0:8}, payload.yaml has $head"
		fi
	fi
done
exit $status
