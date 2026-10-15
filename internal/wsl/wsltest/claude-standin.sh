#!/bin/sh
# LABELLED TEST DOUBLE. This is not Claude Code. Drydock's tests install it as `claude` inside a WSL distro (or inside the
# development host's stand-in distro) to prove the WSL run mode without spending anything. It answers --version and
# `auth status` the way claude 2.1.296 does, and in -p stream-json mode it prints system/init, an assistant message and a
# result for every user message it reads on stdin, and answers an interrupt control request. Its answer describes where it
# runs (working folder, HOME, whether it carries the run marker, which of the files Drydock handed it it could read), so a test
# can check what Drydock gave it. A message containing "spawn" leaves two sleeping grandchildren, one of them in a session of
# its own, so a test can prove that stopping the run kills them. It never prints a value that could be a credential, only the
# length of ANTHROPIC_API_KEY.
case "$1" in
  --version) echo "2.1.296 (Claude Code)"; exit 0 ;;
  auth)
    if [ "$2" = login ]; then
      # the stand-in's interactive sign-in: it says where it runs (the run marker, whether the login shell's PATH reached it,
      # whether its input is a terminal), waits for Enter as a real login waits for the browser, then signs the stand-in in
      if [ -n "$DRYDOCK_WSL_RUN" ]; then mk=yes; else mk=no; fi
      case ":$PATH:" in *":$HOME/.local/bin:"*) lp=yes ;; *) lp=no ;; esac
      if [ -t 0 ]; then tty=yes; else tty=no; fi
      echo "stand-in sign-in (LABELLED TEST DOUBLE): marker=$mk login-path=$lp tty=$tty"
      # a test asks for a helper in a session of its own, as a login's localhost callback listener can be, which outlives the
      # terminal's kill of wsl.exe unless Drydock sweeps the run marker
      if [ -e "$HOME/.drydock-standin-login-helper" ]; then setsid sleep 600 >/dev/null 2>&1 </dev/null & fi
      echo "Press Enter to finish signing in"
      IFS= read -r _ || exit 4
      rm -f "$HOME/.drydock-standin-signed-out"
      echo "Signed in to the stand-in."
      exit 0
    fi
    # a test signs the stand-in out by creating this file in the distro's HOME
    # ... or makes the check itself fail (an error on stderr, no JSON), as a broken install or an unwritable HOME would
    if [ -e "$HOME/.drydock-standin-auth-broken" ]; then echo "Error: EACCES: permission denied, open '$HOME/.claude.json'" >&2; exit 2; fi
    if [ -e "$HOME/.drydock-standin-signed-out" ]; then printf '%s\n' '{"loggedIn": false}'; exit 1; fi
    printf '%s\n' '{"loggedIn": true, "authMethod": "claude.ai", "apiProvider": "firstParty", "email": "standin@example.invalid"}'; exit 0 ;;
esac
mcp=""; prompt=""; n=0
while [ $# -gt 0 ]; do
  case "$1" in
    --mcp-config) mcp="$2" ;;
    --append-system-prompt-file) prompt="$2" ;;
  esac
  n=$((n+1)); shift
done
esc() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }
readable() { if [ -n "$1" ] && [ -r "$1" ]; then echo yes; else echo no; fi; }
mcpcmd=""; mcpcmdok=no
if [ -n "$mcp" ] && [ -r "$mcp" ]; then
  mcpcmd=$(sed -n 's/.*"command": *"\([^"]*\)".*/\1/p' "$mcp" | head -n 1)
  if [ -n "$mcpcmd" ] && [ -e "$mcpcmd" ]; then mcpcmdok=yes; fi
fi
sid="wsl-standin-session"
started=0; turn=0
while IFS= read -r line; do
  case "$line" in
    *'"control_request"'*)
      rid=$(printf '%s' "$line" | sed -n 's/.*"request_id": *"\([^"]*\)".*/\1/p')
      printf '{"type":"control_response","response":{"subtype":"success","request_id":"%s"}}\n' "$rid"
      continue ;;
    *'"type":"user"'*|*'"type": "user"'*) ;;
    *) continue ;;
  esac
  if [ "$started" = 0 ]; then
    printf '{"type":"system","subtype":"init","session_id":"%s","model":"standin","tools":[],"cwd":"%s","claude_code_version":"2.1.296"}\n' "$sid" "$(esc "$PWD")"
    started=1
  fi
  turn=$((turn+1)); extra=""
  case "$line" in
    *spawn*)
      setsid sleep 600 >/dev/null 2>&1 </dev/null &
      sleep 601 >/dev/null 2>&1 </dev/null &
      extra=" spawned=yes" ;;
  esac
  case "$line" in
    *interop*)
      # start the MCP server command the way Claude Code would, with the env block of the config, and report its first line:
      # on Windows this is drydock's Windows program started from Linux through interop, with only what WSLENV names
      if [ "$mcpcmdok" = yes ]; then
        url=$(sed -n 's/.*"DRYDOCK_MCP_URL": *"\([^"]*\)".*/\1/p' "$mcp" | head -n 1)
        tok=$(sed -n 's/.*"DRYDOCK_MCP_TOKEN": *"\([^"]*\)".*/\1/p' "$mcp" | head -n 1)
        we=$(sed -n 's/.*"WSLENV": *"\([^"]*\)".*/\1/p' "$mcp" | head -n 1)
        r=$(DRYDOCK_MCP_URL="$url" DRYDOCK_MCP_TOKEN="$tok" DRYDOCK_INTEROP_CHECK=1 WSLENV="$we:DRYDOCK_INTEROP_CHECK" "$mcpcmd" </dev/null 2>&1 | tr -d '\r' | head -n 1)
        extra="$extra interop=[$r]"
      fi ;;
  esac
  key=${ANTHROPIC_API_KEY:-}
  text="standin turn=$turn cwd=$PWD home=$HOME run=${DRYDOCK_WSL_RUN:+set} keylen=${#key} prompt=$(readable "$prompt") mcp=$(readable "$mcp") mcpcmdok=$mcpcmdok quiet=${CLAUDE_CODE_DISABLE_CRON:-unset} args=$n$extra"
  printf '{"type":"assistant","message":{"id":"msg_%s","type":"message","role":"assistant","model":"standin","content":[{"type":"text","text":"%s"}],"usage":{"input_tokens":1,"output_tokens":1}},"session_id":"%s"}\n' "$turn" "$(esc "$text")" "$sid"
  printf '{"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","result":"%s","session_id":"%s","total_cost_usd":0,"usage":{"input_tokens":1,"output_tokens":1}}\n' "$(esc "$text")" "$sid"
done
