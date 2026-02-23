---
name: route
description: Classify the current conversation context and show the routing decision
---

Analyze the current conversation and determine the optimal model routing.

1. Summarize the current task in one sentence
2. Use the sr-router **route** MCP tool with that summary as the prompt
3. Present the routing decision:
   - **Model**: The selected model
   - **Tier**: Cost tier (premium/budget/speed/free)
   - **Task**: Detected task type
   - **Score**: Routing confidence score
4. If the task seems misclassified, mention what you think it should be
