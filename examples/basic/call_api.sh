#!/bin/bash

helpFunc() {
	echo "Usage: $0 -r"
	echo -e "\t-n Specify how many iterations we call the API."
	echo -e "\t-r Specify request type, see options below:"
	echo -e "\t   - lu: list users without versioning"
	echo -e "\t   - lvu: list users with versioning"
	exit 1
}

n=1

while getopts ":n:r:" opt; do
	case "$opt" in
		n)
			n=$OPTARG
			;;
		r)
			req="$OPTARG"

			if [[ "$req" == "lu" ]]; then
				for ((i=0; i<n; i++)); do
					curl -s localhost:9000/users \
						-H "Content-Type: application/json"  | jq
				done

			elif [[ "$req" == "lvu" ]]; then
				for ((i=0; i<n; i++)); do
					curl -s localhost:9000/users \
						-H "Content-Type: application/json" \
						-H "X-Example-Version: 2023-04-01" | jq
				done

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
