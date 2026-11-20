// The subagent panel: no tab of its own (the shell lights the conversation tab for it), with its
// own head and strip.
import { definePanel } from "@/app/panels";

import { Agent, AgentHead, AgentStrip } from "./Agent";

export const agentPanel = definePanel({
  id: "agent",
  title: () => "Subagent",
  Head: AgentHead,
  Strip: AgentStrip,
  Body: Agent,
});
