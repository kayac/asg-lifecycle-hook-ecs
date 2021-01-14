# asg-lifecycle-hook-ecs

## DESCRIPTION

asg-lifecycle-hook-ecs is a AWS Lambda function to drain ECS container instance when the instance will be terminated by AutoScalingGroup(ASG).

This function is supposed to be called by ASG lifecycle hook.

## Requirements

AutoScalingGroup must have a tag Key="cluster" Value=[ECS cluster name].

asg-lifecycle-hook-ecs detects ECS cluseter name by ASG tag.

## LICENSE

MIT
