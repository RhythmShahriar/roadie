# side-effect.queue: Laravel's queue runs jobs from production (high)

What: QUEUE_CONNECTION isn't sync, null or log and the jobs table has rows.

Why it matters: Queued jobs from production send mail, call APIs and charge
customers.

What Roadie does: Roadie truncates jobs and failed_jobs and sets
QUEUE_CONNECTION=sync, so new jobs run in the request through Echo.

By hand: Stop queue workers on the staging host and set QUEUE_CONNECTION=sync.

