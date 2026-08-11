set -eu
mkdir -p .ai/semport semport
if [ ! -f semport/ledger.tsv ]; then
  printf 'filepath\tiso8601\tdisposition\n' > semport/ledger.tsv
  for f in \
    agent.py agent_output.py agent_tool_input.py agent_tool_state.py \
    exceptions.py items.py result.py run.py run_config.py run_context.py \
    run_state.py run_error_handlers.py model_settings.py usage.py \
    strict_schema.py prompts.py function_schema.py stream_events.py \
    lifecycle.py \
    tool.py tool_context.py tool_guardrails.py \
    guardrail.py \
    handoffs/__init__.py handoffs/history.py \
    extensions/handoff_filters.py extensions/handoff_prompt.py \
    memory/session.py memory/session_settings.py memory/util.py \
    models/interface.py models/multi_provider.py \
    run_internal/run_loop.py run_internal/run_steps.py \
    run_internal/turn_preparation.py run_internal/turn_resolution.py \
    run_internal/tool_execution.py run_internal/tool_actions.py \
    run_internal/tool_planning.py run_internal/tool_use_tracker.py \
    run_internal/guardrails.py run_internal/items.py \
    run_internal/streaming.py run_internal/approvals.py \
    run_internal/error_handlers.py run_internal/session_persistence.py \
    tracing/spans.py tracing/traces.py tracing/context.py \
    tracing/processor_interface.py tracing/span_data.py \
    tracing/create.py tracing/setup.py tracing/scope.py; do
    if [ -f "references/openai-agents-python/src/agents/$f" ]; then
      printf '%s\t%s\tnew\n' "$f" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    fi
  done >> semport/ledger.tsv
  echo "Ledger: $(grep -c 'new' semport/ledger.tsv) files to port"
fi