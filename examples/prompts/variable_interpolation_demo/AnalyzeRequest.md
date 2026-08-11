# Using ctx namespace to access user input
The user requested: ${ctx.human_response}

# Using graph namespace to access workflow metadata
Our workflow goal is: ${graph.goal}
Workflow name: ${graph.name}

Analyze the user's request and determine if it's feasible.
Output STATUS:success if feasible, STATUS:fail otherwise.