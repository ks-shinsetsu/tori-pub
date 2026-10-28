---
id: ex06-interview
format: interview
domain: local government
topic: A free lighthouse museum introducing an admission charge (reserved; excluded from the topic bank)
speakers: 2 (interviewer + curator)
propositions: 10
relations: 7 (RA 4, CA 1, MA 2)
isolated: 1
multi_premise: true (p1 has two supporting premises)
chain: false
words: 157
---

```json
{
  "utterances": [
    {"speaker": "Interviewer", "text": "You have run the lighthouse museum free of charge for twelve years. Why introduce a ticket now?"},
    {"speaker": "Mireille", "text": "We need to charge admission from next spring, because the roof needs replacing and the county grant now covers only staff costs."},
    {"speaker": "Interviewer", "text": "Some would say a charge shuts out the local families who kept the place going."},
    {"speaker": "Mireille", "text": "That is why residents of the parish will keep free entry. The charge is really aimed at the coach tours, who arrive two hundred at a time and spend nothing in the village; that is where the money should come from."},
    {"speaker": "Interviewer", "text": "So, to put it plainly, visitors from outside pay and locals do not."},
    {"speaker": "Mireille", "text": "Exactly. And the tour operators, I should say, have been very understanding so far."},
    {"speaker": "Interviewer", "text": "How much will it be?"},
    {"speaker": "Mireille", "text": "Four pounds. We settled on that because comparable sites along the coast charge between three and six."},
    {"speaker": "Interviewer", "text": "In other words you have pitched it at the middle of the range."}
  ],
  "propositions": [
    {"id": "p1", "utterance": 1, "text": "The lighthouse museum needs to charge admission from next spring."},
    {"id": "p2", "utterance": 1, "text": "The museum's roof needs replacing and the county grant now covers only staff costs."},
    {"id": "p3", "utterance": 2, "text": "An admission charge would shut out the local families who kept the museum going."},
    {"id": "p4", "utterance": 3, "text": "Residents of the parish will keep free entry to the museum."},
    {"id": "p5", "utterance": 3, "text": "The charge is aimed at coach tours, which arrive two hundred at a time and spend nothing in the village."},
    {"id": "p6", "utterance": 4, "text": "Visitors from outside the parish will pay admission and locals will not."},
    {"id": "p7", "utterance": 5, "text": "The tour operators have been understanding about the planned charge."},
    {"id": "p8", "utterance": 7, "text": "Admission to the museum will be four pounds."},
    {"id": "p9", "utterance": 7, "text": "Comparable sites along the coast charge between three and six pounds."},
    {"id": "p10", "utterance": 8, "text": "The museum has pitched its admission charge at the middle of the local range."}
  ],
  "relations": [
    {"kind": "RA", "from": "p2", "to": "p1"},
    {"kind": "CA", "from": "p3", "to": "p1"},
    {"kind": "RA", "from": "p3", "to": "p4"},
    {"kind": "RA", "from": "p5", "to": "p1"},
    {"kind": "MA", "from": "p6", "to": "p4"},
    {"kind": "RA", "from": "p9", "to": "p8"},
    {"kind": "MA", "from": "p10", "to": "p8"}
  ]
}
```

Notes: the interviewer's reported objection ("Some would say ...") is a proposition and attacks p1; the curator's reply treats it as the *reason* for p4 ("That is why"), so p3 has both an outgoing CA and an outgoing RA. Questions are not propositions. Both MAs are restatements by the interviewer.
