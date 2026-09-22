#!/bin/sh
# Serves this instance's own brand in the console's first paint.
#
# The console image is one build for any instance, so a brand cannot be baked
# into index.html. It lives in exactly one place -- the deploy's platform.brand,
# which erun-backend-api serves as GET /v1/platform's "brand" -- and this script
# applies that same value to the static page: the title and social tags that
# render before the bundle runs, and the window value the bundle itself reads
# (erun-console/src/shell/brand.ts). Both sides drawn from one value means
# discovery resolving a moment later cannot change what the page already shows.
#
# Unset leaves the bundled product-level default in index.html, which is also
# what an instance that configures no brand reports at GET /v1/platform -- so
# the two still agree rather than naming two different products.
set -eu

brand="${ERUN_PLATFORM_BRAND:-}"
[ -n "$brand" ] || exit 0

html=/usr/share/nginx/html/index.html
[ -f "$html" ] || exit 0

# A brand is operator-configured text, so escape it for each context it lands
# in rather than assuming it is a bare word: HTML text (the title), then sed's
# own replacement syntax for both.
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

# index.html's <title>/<meta> shapes are fixed by the source file (see its
# comment); unset, none of these patterns match and the file is left alone.
sed -i \
    -e "s#<title>[^<]*</title>#<title>${label}</title>#" \
    -e "s#content=\"[^\"]*\" property=\"og:title\"#content=\"${label}\" property=\"og:title\"#" \
    -e "s#content=\"[^\"]*\" name=\"twitter:title\"#content=\"${label}\" name=\"twitter:title\"#" \
    -e "s#</head>#<script>window.__ERUN_PLATFORM_BRAND__='${injected}';</script></head>#" \
    "$html"
