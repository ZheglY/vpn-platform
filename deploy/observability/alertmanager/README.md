# Alertmanager routing baseline

`alertmanager.yml` is an inert, credential-free routing baseline. Its receivers intentionally contain no integration configuration, URL, token, password, or address.

An environment owner must add approved HA Alertmanager instances and receiver integrations in an environment-specific secret-bearing overlay. The overlay may reference the reviewed `vpn.title` and `vpn.body` templates, but must not be committed. Prometheus is not pointed at this inert file, so local alerts cannot be mistaken for delivered production notifications.
