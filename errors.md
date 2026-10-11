Deployments that hang or end in the wrong state
1. Endless tofu loop (H2). With two managed dependencies, or after configuring
   the same one twice, tofu.apply reruns forever (47 commands in 5 s in thetest). The cause is that ScheduleCommands reschedules every pending dependency without checking command_id (server/internal/service/dependency.go:156).
2. All-unmanaged dependencies never deploy (H3). The deployment stays pending and helm never runs.
3. Failures after the dependencies succeed are swallowed (H1, H4). A template render error, a tofu output that isn't a JSON object, or a DB error on the
   configure path is only logged. The deployment stays pending with no error. 
4. Cancel isn't respected (H13).
   - A late helm result turns cancelled into succeeded.
   - A late tofu result revives the deployment and runs helm.
   - Configure after cancel restarts the deployment.
   - Dispatched commands keep running after a cancel.
6. Writes that aren't atomic (H11, H12).- A no-deps submit with a template that can't resolve returns 500 after the
     commit, leaves an orphan pending row, and each retry adds another.
   - If creating the helm command fails after its ID is recorded, GET /deployments/:id returns 404 from then on.
7. Configure isn't transactional. A rejected request can still write some dependency rows. It also returns 204 for a deployment that doesn't exist.

Agent and dispatch
8. An output line over 64 KiB hangs the agent forever (H6), which stalls that
cluster's whole queue.
9. One agent's disconnect requeues every in-flight command on the cluster, including commands other agents are running, so helm and tofu run twice. The requeued command also doesn't wake an idle agent.
10. The session's "busy" flag drops messages. A result arriving on a connection that isn't busy (e.g. after a reconnect) is dropped, and so is a dispatch arriving while it is busy. The command stays dispatched forever.                                                                           Secrets 11. Secrets are logged at Info. The server logs the submit body, includingand config values. The agent logs every helm/tofu output line throughslog.Default(), ignoring LOG_LEVEL.
12. The helm values temp file is never deleted, so rendered secrets are left in /tmp.

CLI
13. deploy --wait is wrong on two non-success states. It loops forever on cancelled, and it exits 0 on awaiting_input.
14. configure claims success without configuring anything. Given a file without the dependencies wrapper, it prints "configured successfully" and nothing is configured.
15. status misreports. It shows awaiting-input dependencies as [manual], and neither status nor --wait ever prints the deployment's own error.
16. A server URL with a trailing slash breaks every request. I confirmed it: the request goes to //api/v1/... and gets a 404. context add stores the URL as given.
17. Parse errors say pond.yml even when -f custom.yml was used.

Smaller
18. List pagination repeats an item: the last item of one page appears again on the next.
19. Template values inside YAML lists are never rendered, so {{db.host}} reaches the app as literal text.
20. Status fields that are never updated.
    - The running status is never set, although the comment on handleCommandStarted says it is.
    - The dependency completedAt field is never filled in.
    - Cancelling twice succeeds and moves completed_at.
