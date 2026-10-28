---
id: ex02-debate-large
format: TV debate with a moderator
domain: economy
topic: Scrapping first-class carriages on the national rail operator (reserved; excluded from the topic bank)
speakers: 5 (host + 3 panellists + 1 audience member)
propositions: 18
relations: 14 (RA 8, CA 2, MA 4)
isolated: 3
multi_premise: true (p2 has three supporting premises)
chain: true (p4 → p3 → p2)
words: 304
---

```json
{
  "utterances": [
    {"speaker": "Host", "text": "Our next question comes from Hannah, in the third row."},
    {"speaker": "Hannah", "text": "Should the national rail operator scrap first-class carriages altogether?"},
    {"speaker": "Host", "text": "Oskar, your union has been campaigning for exactly that."},
    {"speaker": "Oskar", "text": "Standard class is overcrowded on every evening service out of the capital, while the first-class seats sit empty. So the partition should go."},
    {"speaker": "Host", "text": "Is that the whole case?"},
    {"speaker": "Oskar", "text": "No. Removing it would also add roughly a fifth more seats to every train, because a first-class carriage seats twenty-four where a standard one seats sixty."},
    {"speaker": "Host", "text": "Callum."},
    {"speaker": "Callum", "text": "Those empty seats are not empty in the accounts, though: first-class fares bring in around fifteen percent of revenue on the long-distance routes. For what it is worth, I travel standard myself."},
    {"speaker": "Callum", "text": "That is why scrapping them would mean either higher standard fares or a bigger subsidy."},
    {"speaker": "Bea", "text": "Our survey of twelve thousand passengers found that only one in twenty had bought a first-class ticket in the past year, which bears out what Oskar said about the empty seats."},
    {"speaker": "Bea", "text": "But the real problem is not the partition, it is the timetable, because most of the overcrowding is on three specific routes."},
    {"speaker": "Host", "text": "So, Bea, you are saying the carriages are a distraction from the real fix."},
    {"speaker": "Bea", "text": "Exactly. The partition is a symptom, not the disease."},
    {"speaker": "Oskar", "text": "Put another way, the overcrowding is concentrated where the trains are fullest, and that is precisely where the extra seats would help, so the case for removing it stands."},
    {"speaker": "Callum", "text": "Let me put the revenue point another way: a first-class ticket is a voluntary tax paid by people who can afford it."},
    {"speaker": "Hannah", "text": "I commute daily and I have never once seen the first-class carriage more than a quarter full, which backs up the empty-seats point."},
    {"speaker": "Host", "text": "Last word to Bea."},
    {"speaker": "Bea", "text": "Whatever we decide, the trains themselves are thirty years old, and the new fleet order is already two years late."}
  ],
  "propositions": [
    {"id": "p1", "utterance": 3, "text": "Standard class is overcrowded on evening services out of the capital while first-class seats sit empty."},
    {"id": "p2", "utterance": 3, "text": "The first-class partition should be removed."},
    {"id": "p3", "utterance": 5, "text": "Removing the partition would add roughly a fifth more seats to every train."},
    {"id": "p4", "utterance": 5, "text": "A first-class carriage seats twenty-four people where a standard carriage seats sixty."},
    {"id": "p5", "utterance": 7, "text": "First-class fares bring in around fifteen percent of revenue on long-distance routes."},
    {"id": "p6", "utterance": 7, "text": "Callum travels standard class himself."},
    {"id": "p7", "utterance": 8, "text": "Scrapping first class would mean either higher standard fares or a bigger subsidy."},
    {"id": "p8", "utterance": 9, "text": "Only one in twenty of twelve thousand surveyed passengers had bought a first-class ticket in the past year."},
    {"id": "p9", "utterance": 10, "text": "The real cause of overcrowding is the timetable, not the first-class partition."},
    {"id": "p10", "utterance": 10, "text": "Most of the overcrowding is on three specific routes."},
    {"id": "p11", "utterance": 11, "text": "The first-class carriages are a distraction from the real fix for overcrowding."},
    {"id": "p12", "utterance": 12, "text": "The first-class partition is a symptom of overcrowding, not its cause."},
    {"id": "p13", "utterance": 13, "text": "Overcrowding is concentrated on the routes where trains are fullest."},
    {"id": "p14", "utterance": 13, "text": "The extra seats from removing the partition would help most on the busiest routes."},
    {"id": "p15", "utterance": 14, "text": "A first-class ticket is a voluntary tax paid by people who can afford it."},
    {"id": "p16", "utterance": 15, "text": "Hannah has never seen the first-class carriage more than a quarter full on her daily commute."},
    {"id": "p17", "utterance": 17, "text": "The current trains are thirty years old."},
    {"id": "p18", "utterance": 17, "text": "The new fleet order is already two years late."}
  ],
  "relations": [
    {"kind": "RA", "from": "p1", "to": "p2"},
    {"kind": "RA", "from": "p3", "to": "p2"},
    {"kind": "RA", "from": "p4", "to": "p3"},
    {"kind": "CA", "from": "p5", "to": "p2"},
    {"kind": "RA", "from": "p5", "to": "p7"},
    {"kind": "RA", "from": "p8", "to": "p1"},
    {"kind": "CA", "from": "p9", "to": "p2"},
    {"kind": "RA", "from": "p10", "to": "p9"},
    {"kind": "MA", "from": "p11", "to": "p9"},
    {"kind": "MA", "from": "p12", "to": "p11"},
    {"kind": "MA", "from": "p13", "to": "p10"},
    {"kind": "RA", "from": "p14", "to": "p2"},
    {"kind": "MA", "from": "p15", "to": "p5"},
    {"kind": "RA", "from": "p16", "to": "p1"}
  ]
}
```

Notes: three of the four MA relations are restatements by a different speaker (host → Bea, Bea → host, Oskar → Bea); one is a self-restatement (Callum). p5 both attacks the conclusion p2 and supports p7, which is normal. p6, p17 and p18 are isolated. The question in utterance 1 and the host's moderation are not propositions.
