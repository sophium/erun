#!/bin/sh
# Applies platform.brand to index.html at start so the first paint matches GET /v1/platform.
set -eu

brand="${ERUN_PLATFORM_BRAND:-}"
[ -n "$brand" ] || exit 0

html=/usr/share/nginx/html/index.html
[ -f "$html" ] || exit 0

# The brand is operator text: escape it for HTML/JS, then for sed's replacement.
html_text() {
    printf '%s' "$1" | sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g' -e 's/"/\&quot;/g'
}
js_string() {
    printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e "s/'/\\\\'/g"
}
sed_escape() {
    printf '%s' "$1" | sed -e 's/[\\&#]/\\&/g'
}

label=$(sed_escape "$(html_text "$brand console")")
injected=$(sed_escape "$(js_string "$brand")")

# These patterns rely on index.html's fixed <title>/<meta> shapes.
sed -i \
    -e "s#<title>[^<]*</title>#<title>${label}</title>#" \
    -e "s#content=\"[^\"]*\" property=\"og:title\"#content=\"${label}\" property=\"og:title\"#" \
    -e "s#content=\"[^\"]*\" name=\"twitter:title\"#content=\"${label}\" name=\"twitter:title\"#" \
    -e "s#</head>#<script>window.__ERUN_PLATFORM_BRAND__='${injected}';</script></head>#" \
    "$html"
