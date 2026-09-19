#!/bin/sh
# Prepares the example network "lab" through ghost-server's control API.
# It runs once; later runs find /shared/done and exit.
set -eu

S=${GHOST_SERVER:-http://ghost-server:8080}
if [ -f /shared/done ]; then
  echo "setup: already done"
  exit 0
fi

api() {
  curl -fsS -H "Authorization: Bearer $GHOST_CONTROL_TOKEN" -H "Content-Type: application/json" "$@"
}

# The policy: hubs reach every peer (the default ACL), and peers tagged
# tag:exit may exit to $EXIT_ALLOW only. The network is hub-only, so nodes
# never see each other.
jq -n --arg allow "$EXIT_ALLOW" '{
  tags: {"tag:exit": {description: "example exits"}},
  acls: [{action: "accept", src: ["role:hub"], dst: ["*"]}],
  exit: [{name: "web", target: ["tag:exit"], allow: ($allow | split(","))}]
}' | api -X PUT "$S/control/networks/lab/policy" -d @- >/dev/null
echo "setup: exit policy allows $EXIT_ALLOW"

# Hubs are infrastructure: create them directly. The credentials are what
# ghost-cli reads with -creds; add the server so no -server flag is needed.
for hub in hub hub-cli; do
  api -X POST "$S/control/networks/lab/peers" -d "{\"name\":\"$hub\",\"roles\":[\"hub\"]}" |
    jq --arg s "$S" '. + {server: $s}' >"/shared/$hub.json"
  echo "setup: created $hub ($(jq -r .peer_id "/shared/$hub.json"))"
done

# The node enrols itself with a single-use pre-auth key that grants the exit
# role and tag.
api -X POST "$S/control/networks/lab/auth-keys" \
  -d '{"roles":["node","exit"],"tags":["tag:exit"],"expires_in_seconds":3600}' |
  jq -r .key >/shared/node.authkey
echo "setup: created a pre-auth key for the node"

chmod 0644 /shared/*.json /shared/node.authkey
touch /shared/done
