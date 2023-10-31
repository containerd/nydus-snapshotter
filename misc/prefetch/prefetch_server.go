package main

import (
	"fmt"
	"net/http"
)

func getWordpressPrefetchList(w http.ResponseWriter, req *http.Request) {
	responseText :=
		`/usr/src/wordpress/wp-includes/js/imgareaselect/jquery.imgareaselect.min.js
/usr/src/wordpress/wp-includes/js/imgareaselect/imgareaselect.css
/usr/src/wordpress/wp-includes/js/imgareaselect/border-anim-v.gif
/usr/src/wordpress/wp-includes/js/wp-emoji.js
/usr/src/wordpress/wp-includes/js/wp-util.min.js
/usr/src/wordpress/wp-includes/js/media-editor.min.js
/usr/src/wordpress/wp-includes/js/wp-lists.min.js
/usr/src/wordpress/wp-includes/js/wp-lists.js
/usr/src/wordpress/wp-includes/js/shortcode.js
/usr/src/wordpress/wp-includes/js/wp-embed.js
/usr/src/wordpress/wp-includes/js/customize-loader.js
/usr/src/wordpress/wp-includes/js/hoverIntent.min.js
/usr/src/wordpress/wp-includes/js/codemirror/esprima.js
/usr/src/wordpress/wp-includes/js/codemirror/jsonlint.js
/usr/src/wordpress/wp-includes/js/codemirror/codemirror.min.js
/usr/src/wordpress/wp-includes/js/codemirror/codemirror.min.css
/usr/src/wordpress/wp-includes/js/codemirror/fakejshint.js
/usr/src/wordpress/wp-includes/js/codemirror/htmlhint-kses.js
/usr/src/wordpress/wp-includes/js/codemirror/csslint.js
/usr/src/wordpress/wp-includes/js/codemirror/htmlhint.js
/usr/src/wordpress/wp-includes/js/wp-auth-check.min.js
/usr/src/wordpress/wp-includes/js/customize-preview-widgets.min.js
/usr/src/wordpress/wp-includes/js/json2.js
/usr/src/wordpress/wp-includes/js/wp-ajax-response.js
/usr/src/wordpress/wp-includes/js/zxcvbn-async.js
/usr/src/wordpress/wp-includes/js/tinymce/license.txt`

	fmt.Fprintf(w, responseText)
}

func getTomcatPrefetchList(w http.ResponseWriter, req *http.Request) {
	responseText :=
		`/usr/bin/bash
/usr/lib/x86_64-linux-gnu/ld-linux-x86-64.so.2
/etc/ld.so.cache
/usr/lib/x86_64-linux-gnu/libtinfo.so.6.3
/usr/lib/x86_64-linux-gnu/libc.so.6
/usr/lib/locale/locale-archive
/usr/lib/x86_64-linux-gnu/gconv/gconv-modules.cache
/etc/nsswitch.conf
/etc/passwd
/usr/bin/curl
/usr/lib/x86_64-linux-gnu/libcurl.so.4.7.0
/usr/lib/x86_64-linux-gnu/libz.so.1.2.11
/usr/lib/x86_64-linux-gnu/libnghttp2.so.14.20.1
/usr/lib/x86_64-linux-gnu/libidn2.so.0.3.7
/usr/lib/x86_64-linux-gnu/librtmp.so.1
/usr/lib/x86_64-linux-gnu/libssh.so.4.8.7
/usr/lib/x86_64-linux-gnu/libpsl.so.5.3.2
/usr/lib/x86_64-linux-gnu/libssl.so.3
/usr/lib/x86_64-linux-gnu/libcrypto.so.3
/usr/lib/x86_64-linux-gnu/libgssapi_krb5.so.2.2
/usr/lib/x86_64-linux-gnu/libldap-2.5.so.0.1.11
/usr/lib/x86_64-linux-gnu/liblber-2.5.so.0.1.11
/usr/lib/x86_64-linux-gnu/libzstd.so.1.4.8
/usr/lib/x86_64-linux-gnu/libbrotlidec.so.1.0.9
/usr/lib/x86_64-linux-gnu/libunistring.so.2.2.0
/usr/lib/x86_64-linux-gnu/libgnutls.so.30.31.0
/usr/lib/x86_64-linux-gnu/libhogweed.so.6.4
/usr/lib/x86_64-linux-gnu/libnettle.so.8.4
/usr/lib/x86_64-linux-gnu/libgmp.so.10.4.1
/usr/lib/x86_64-linux-gnu/libkrb5.so.3.3
/usr/lib/x86_64-linux-gnu/libk5crypto.so.3.1
/usr/lib/x86_64-linux-gnu/libcom_err.so.2.1
/usr/lib/x86_64-linux-gnu/libkrb5support.so.0.1
/usr/lib/x86_64-linux-gnu/libsasl2.so.2.0.25
/usr/lib/x86_64-linux-gnu/libbrotlicommon.so.1.0.9
/usr/lib/x86_64-linux-gnu/libp11-kit.so.0.3.0
/usr/lib/x86_64-linux-gnu/libtasn1.so.6.6.2
/usr/lib/x86_64-linux-gnu/libkeyutils.so.1.9
/usr/lib/x86_64-linux-gnu/libresolv.so.2
/usr/lib/x86_64-linux-gnu/libffi.so.8.1.0
/etc/ssl/openssl.cnf
/etc/locale.alias
/usr/bin/sleep`

	fmt.Fprintf(w, responseText)
}

func main() {

	http.HandleFunc("/prefetch/wordpress", getWordpressPrefetchList)
	http.HandleFunc("/prefetch/tomcat", getTomcatPrefetchList)
	http.ListenAndServe(":8090", nil)
}
