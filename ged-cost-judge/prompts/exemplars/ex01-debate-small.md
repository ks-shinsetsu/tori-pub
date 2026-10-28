---
id: ex01-debate-small
format: TV debate with a moderator
domain: local government
topic: Replacing a town's summer fireworks with a drone light show (reserved; excluded from the topic bank)
speakers: 4 (host + 2 panellists + 1 audience member)
propositions: 11
relations: 7 (RA 4, CA 1, MA 2)
isolated: 1
multi_premise: false
chain: true
words: 182
---

```json
{
  "utterances": [
    {"speaker": "Host", "text": "Tonight's question: should Harlow Bay replace its summer fireworks with a drone light show? Dev, you put the proposal to the council."},
    {"speaker": "Dev", "text": "Fireworks frighten the dogs and horses kept along the bay. The rescue centre logged forty call-outs on the night last year, so the harm is real, not hypothetical."},
    {"speaker": "Host", "text": "Marianne, you speak for the harbour traders."},
    {"speaker": "Marianne", "text": "The display is the one night a year the seafront cafés fill up. Because of that, the traders would lose their best takings of the summer if it went."},
    {"speaker": "Dev", "text": "But a drone show draws a crowd just as well. The one in Ashcombe sold out two nights running."},
    {"speaker": "Host", "text": "So, Marianne, in other words Dev is saying your cafés would still be full."},
    {"speaker": "Marianne", "text": "Perhaps. That is to say, the crowd would come for the lights rather than the bangs. I also think the fireworks are simply part of what this town is."},
    {"speaker": "Tom", "text": "Can I add something from the floor? Drones cost about twice what fireworks do, so the switch is not affordable this year."},
    {"speaker": "Host", "text": "Thank you. We will take a show of hands after the break."}
  ],
  "propositions": [
    {"id": "p1", "utterance": 1, "text": "Fireworks frighten the dogs and horses kept along Harlow Bay."},
    {"id": "p2", "utterance": 1, "text": "The rescue centre logged forty call-outs on the night of last year's fireworks."},
    {"id": "p3", "utterance": 3, "text": "The fireworks display is the one night a year the seafront cafés fill up."},
    {"id": "p4", "utterance": 3, "text": "The traders would lose their best takings of the summer if the fireworks display went."},
    {"id": "p5", "utterance": 4, "text": "A drone show draws a crowd just as well as fireworks."},
    {"id": "p6", "utterance": 4, "text": "The drone show in Ashcombe sold out two nights running."},
    {"id": "p7", "utterance": 5, "text": "The seafront cafés would still be full on the night of a drone show."},
    {"id": "p8", "utterance": 6, "text": "People would come to the seafront for the lights rather than for the bangs."},
    {"id": "p9", "utterance": 6, "text": "The fireworks are part of what Harlow Bay is."},
    {"id": "p10", "utterance": 7, "text": "Drones cost about twice what fireworks do."},
    {"id": "p11", "utterance": 7, "text": "Switching to a drone show is not affordable this year."}
  ],
  "relations": [
    {"kind": "RA", "from": "p2", "to": "p1"},
    {"kind": "RA", "from": "p3", "to": "p4"},
    {"kind": "CA", "from": "p5", "to": "p4"},
    {"kind": "RA", "from": "p6", "to": "p5"},
    {"kind": "MA", "from": "p7", "to": "p5"},
    {"kind": "MA", "from": "p8", "to": "p7"},
    {"kind": "RA", "from": "p10", "to": "p11"}
  ]
}
```

Notes: both MA relations are restatements by a *different* speaker (host restating Dev; Marianne restating the host). p9 is deliberately isolated. The CA is signalled by "But", the RAs by "so", "Because of that", a bare causal sentence pair in utterance 4, and "so" in utterance 7.
