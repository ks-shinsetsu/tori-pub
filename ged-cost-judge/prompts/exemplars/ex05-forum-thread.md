---
id: ex05-forum-thread
format: online forum thread
domain: science
topic: Whether an amateur astronomy club should buy one shared telescope or subsidise members' own (reserved; excluded from the topic bank)
speakers: 4 (handles)
propositions: 11
relations: 8 (RA 5, CA 1, MA 2)
isolated: 2
multi_premise: true (p1 and p5 each have two supporting premises)
chain: true (p8 → p9 → p1)
words: 166
---

```json
{
  "utterances": [
    {"speaker": "quasar_pete", "text": "Proposal for the AGM: the club should buy one good 12-inch telescope for shared use rather than subsidising members' own purchases. The reason is that a shared instrument gets used every clear night, whereas subsidised scopes end up in cupboards."},
    {"speaker": "m.okafor", "text": "Agree with the OP. To put it another way: one scope used thirty nights a year beats ten scopes used three."},
    {"speaker": "Lindy_B", "text": "Except that most members live forty minutes from the hut, so a shared scope would only ever be used by the same five people."},
    {"speaker": "m.okafor", "text": "Fair. The Tuesday sessions are already the same five faces, which bears that out."},
    {"speaker": "starling77", "text": "Side note: the car park lights are back on again and they ruin the eastern sky."},
    {"speaker": "quasar_pete", "text": "On cost: the club has eleven thousand in the account, so one 12-inch is affordable outright, which is why I say buy."},
    {"speaker": "Lindy_B", "text": "For the record, I would rather we spent it on the hut roof. But I will go with the majority."},
    {"speaker": "m.okafor", "text": "So the thread's view is: buy the shared scope."}
  ],
  "propositions": [
    {"id": "p1", "utterance": 0, "text": "The club should buy one shared 12-inch telescope rather than subsidise members' own purchases."},
    {"id": "p2", "utterance": 0, "text": "A shared telescope gets used every clear night, whereas subsidised telescopes end up in cupboards."},
    {"id": "p3", "utterance": 1, "text": "One telescope used thirty nights a year is better than ten telescopes used three nights each."},
    {"id": "p4", "utterance": 2, "text": "Most club members live forty minutes from the observatory hut."},
    {"id": "p5", "utterance": 2, "text": "A shared telescope would only ever be used by the same five people."},
    {"id": "p6", "utterance": 3, "text": "The Tuesday observing sessions are already attended by the same five people."},
    {"id": "p7", "utterance": 4, "text": "The car park lights are on again and ruin the view of the eastern sky."},
    {"id": "p8", "utterance": 5, "text": "The club has eleven thousand in its account."},
    {"id": "p9", "utterance": 5, "text": "One 12-inch telescope is affordable outright."},
    {"id": "p10", "utterance": 6, "text": "Lindy_B would rather the money were spent on the hut roof."},
    {"id": "p11", "utterance": 7, "text": "The thread's view is that the club should buy the shared telescope."}
  ],
  "relations": [
    {"kind": "RA", "from": "p2", "to": "p1"},
    {"kind": "MA", "from": "p3", "to": "p2"},
    {"kind": "RA", "from": "p4", "to": "p5"},
    {"kind": "CA", "from": "p5", "to": "p1"},
    {"kind": "RA", "from": "p6", "to": "p5"},
    {"kind": "RA", "from": "p8", "to": "p9"},
    {"kind": "RA", "from": "p9", "to": "p1"},
    {"kind": "MA", "from": "p11", "to": "p1"}
  ]
}
```

Notes: written style (handles, "OP", "Side note"). The off-topic post (p7) is a realistic isolated proposition. Both MAs are by a different poster than the one restated.
