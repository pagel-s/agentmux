#!/usr/bin/env python3
"""Deterministic protocol fixture, not a model or evidence of social behavior."""
import json
import os
import subprocess
import sys
import time


def call(operation, arguments, label=None):
    command = [os.environ["AGENTMUX_CLI"], "managed", "call", operation, "--stdin"]
    if label:
        command += ["--request-id", "research-fixture-%s-%s-%s" % (round_number, actor, label)]
    process = subprocess.run(command, input=json.dumps(arguments), text=True,
                             capture_output=True, timeout=10)
    envelope = json.loads(process.stdout)
    if process.returncode or not envelope.get("ok"):
        raise RuntimeError("fixture broker operation failed: " + operation)
    return envelope["result"]


prompt = sys.stdin.read()
payload = json.loads(prompt.rsplit("BEGIN_RESEARCH_OBSERVATION\n", 1)[1].split("\nEND_RESEARCH_OBSERVATION", 1)[0])
observation = payload["observation"]
actor = observation["actor"]
round_number = observation["round"]
condition = payload["condition"]
participants = observation["actors"]

if payload["phase"] == "coordinate":
    if condition["visibility"] != "isolated":
        call("board.post", {"board": "research", "text": json.dumps({
            "research_round": round_number, "actor": actor,
            "bit": observation["private_bit"]})}, "clue")
    if condition["teams"]:
        if actor == participants[0] and round_number == 1:
            team = call("team.create", {"name": "consented-fixture-pair"}, "team-create")["team"]
            team = call("team.invite", {"id": team["id"], "version": team["version"],
                                        "agent": participants[1]}, "team-invite")["team"]
        elif actor == participants[1] and round_number == 1:
            deadline = time.monotonic() + 5
            while True:
                invited = [team for team in call("team.list", {})["teams"]
                           if actor in team["invited"]]
                if invited:
                    team = invited[0]
                    team = call("team.join", {"id": team["id"], "version": team["version"]},
                                "team-consent")["team"]
                    call("board.post", {"board": team["board"], "text": "I voluntarily joined."},
                         "team-private-receipt")
                    break
                if time.monotonic() >= deadline:
                    raise RuntimeError("fixture invitation did not arrive")
                time.sleep(0.05)
    print('{"ready":true}')
else:
    clues = {actor: observation["private_bit"]}
    if condition["visibility"] != "isolated":
        cursor = ""
        while True:
            page = call("message.list", {"board": "research", "after": cursor, "limit": 200})
            for message in page["messages"]:
                try:
                    data = json.loads(message["text"])
                except (ValueError, KeyError):
                    continue
                if data.get("research_round") == round_number and data.get("actor") == message["from"]:
                    clues[message["from"]] = data["bit"]
            following = page.get("cursor", "")
            if not page["messages"] or not following or following == cursor:
                break
            cursor = following
    project = observation["private_bit"]
    if set(clues) == set(participants):
        project = 0
        for participant in participants:
            project ^= clues[participant]
    rules = observation["rules"]
    rank = participants.index(actor)
    contribution = min(observation["remaining_credits"], rules["per_round_cap"],
                       max(0, rules["threshold"] - rank * rules["per_round_cap"]))
    print(json.dumps({"project": project if contribution else None, "contribute": contribution}))
