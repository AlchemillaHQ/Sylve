#!/bin/sh

MD_DIR="."
# Needed vars: WIKI_URL, API_KEY

# Loop through md files
for md in $MD_DIR/*.md; do
	page="${md##*/}"
	page="${page%.[0-9].md}"
	page="${page%.md}"
	wikipath="man/${page}"

	# Grab the ID of the page
	get_id='{"query":"{ pages { singleByPath (locale: \"en\", path: \"'${wikipath}'\") { id, updatedAt } } }"}'
	set -- $(curl -sX POST "$WIKI_URL" \
		-H "Authorization: Bearer $API_KEY" \
		-H "Content-Type: application/json" \
		-d "$get_id" | jq -r '(.data.pages.singleByPath.id, .data.pages.singleByPath.updatedAt)')
	id=$1
	updated=$2

	# Skip if the page doesn't exit
	if [ "$id" = "null" ] ;then
		echo No wiki page for ${page}
		continue
	fi

	# Make sure it hasn't been updated
	wiki_time=$(date -ju -f "%Y-%m-%dT%H:%M:%S" "${updated%.*}" "+%s")
	file_time=$(stat -f "%m" "${md}")
	if [ "${wiki_time}" -gt "${file_time}" ]; then
		echo "Skipping ${md}: Wiki page is newer (${updated})"
		continue
	fi

	# Upload the markdown file
	echo Updating ${page}, ID ${id}:
	jq -n \
		--arg id "$id" \
		--rawfile content "$md" \
		'{
			query: "mutation { pages { update(
				id: \($id | tonumber),
				content: \($content | @json),
				isPublished: true,
				tags: [],
				editor: \"markdown\"
			) { responseResult { succeeded errorCode message } } } }"
		}' | \
	curl -sX POST "$WIKI_URL" -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" -d @- | \
		jq .data.pages.update.responseResult.message

	# Render the page
	echo Rendering:
	render='{"query":"mutation { pages { render (id: '${id}') { responseResult { errorCode, message } } } }"}'
	curl -sX POST "$WIKI_URL" -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" \
		-d "$render"|jq .data.pages.render.responseResult.message
done
