# Using params namespace - these values come from parent's params attribute
Task Description: ${params.task_description}
Severity Level: ${params.severity_level}
Using Model: ${params.preferred_model}

# Can also access graph namespace
Subgraph Goal: ${graph.goal}

Execute the task with the specified parameters.
Output STATUS:success when complete.