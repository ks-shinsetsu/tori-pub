# Superseded A.5 rows (code 3f8c8d5)

Null world, seeds 101–110, B = 200, d-reps 200, 750 sources. Run with the CycleTol = 0.01
validity rule, under which 4/10 seeds (103, 104, 105, 110) had > 5% invalid bootstrap replicates,
so the normalization scale was not identified and every normalized decision was blanked
(uncapped "validated" 6/10). The cause was a boundary degeneracy of the matching when the last
knot increment reaches 0; it was fixed in 68848ef (tie-break toward the interior matching; a
cycle is a valid iterate, as pre-registered in decisions-stats §11). Kept as a record of the
revision; not used in any rate. The current A.5 rows are one directory up.
