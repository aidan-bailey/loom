// The pair panel: no head and no tab, and its body stays in the entry because an unpaired launch
// opens it.
import { definePanel } from "@/app/panels";

import { Pair } from "./Pair";

export const pairPanel = definePanel({ id: "pair", title: () => "Pairing", Body: Pair });
