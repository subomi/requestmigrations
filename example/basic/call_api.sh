#!/bin/bash

helpFunc() {
	echo "Usage: $0 -r"
	echo -e "\t-r Specify request type, see options below:"
	echo -e "\t   - lu: list users without versioning"
	echo -e "\t   - lvu: list users with versioning"
	exit 1
}

while getopts ":r:" opt; do
	case "$opt" in
		r)
			req="$OPTARG"

			if [[ "$req" == "lu" ]]; then
				curl -s localhost:9000/users \
					-H "Content-Type: application/json"  | jq

			elif [[ "$req" == "lvu" ]]; then
				curl -s localhost:9000/users \
					-H "Content-Type: application/json" \
					-H "X-Example-Version: 2023-09-02" | jq

			else
				helpFunc
			fi
			;;
		?) helpFunc ;;
		:) helpFunc ;;
	esac
done

if [ -z "$req" ]; then
	echo "Missing required argument: -r req">&2
	exit 1
fi
