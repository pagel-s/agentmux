#!/bin/sh

set -eu

tools_dir=${1:-}
min_version=${2:-1.24}

if [ -z "$tools_dir" ] || [ ! -d "$tools_dir" ]; then
	printf ''
	exit 0
fi

min_minor=${min_version#1.}
min_minor=${min_minor%%.*}
best=
best_minor=0
best_patch=0

for dir in "$tools_dir"/go1.*; do
	[ -d "$dir" ] || continue
	[ -x "$dir/bin/go" ] || continue

	name=${dir##*/go}
	case "$name" in
		1.[0-9]*|1.[0-9]*.[0-9]*)
			minor=${name#1.}
			minor=${minor%%.*}
			case "$minor" in
				''|*[!0-9]*) continue ;;
			esac

			patch=${name#1.$minor}
			patch=${patch#.}
			case "$patch" in
				''|*[!0-9]*) patch=0 ;;
			esac

			if [ "$minor" -lt "$min_minor" ]; then
				continue
			fi

			if [ -z "$best" ] || [ "$minor" -gt "$best_minor" ] || {
				[ "$minor" -eq "$best_minor" ] && [ "$patch" -gt "$best_patch" ]
			}; then
				best=$dir/bin/go
				best_minor=$minor
				best_patch=$patch
			fi
			;;
	esac
done

printf '%s' "$best"
