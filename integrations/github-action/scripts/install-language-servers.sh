#!/usr/bin/env bash
# Opt-in installs for the LSP-backed --precise resolvers. codemap degrades
# honestly without these (name-based call graph for TS/JS, `unresolved` for
# Python) and the render script surfaces that degradation in the comment, so a
# consuming repo pays this cost only if it asks for it.
set -euo pipefail

: "${INSTALL_TS_LANGUAGE_SERVER:=false}"
: "${INSTALL_PYRIGHT:=false}"

if [[ "$INSTALL_TS_LANGUAGE_SERVER" == "true" ]]; then
  if command -v typescript-language-server >/dev/null 2>&1; then
    echo "codemap-action: typescript-language-server already on PATH"
  else
    echo "codemap-action: installing typescript-language-server + typescript"
    npm install -g typescript-language-server typescript
  fi
fi

if [[ "$INSTALL_PYRIGHT" == "true" ]]; then
  if command -v pyright-langserver >/dev/null 2>&1; then
    echo "codemap-action: pyright-langserver already on PATH"
  else
    echo "codemap-action: installing pyright"
    npm install -g pyright
  fi
fi
