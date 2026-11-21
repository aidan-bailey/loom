import { useEffect, useId, useState, type KeyboardEvent, type ReactElement } from "react";

import { CommandButton } from "@/app/CommandButton";
import { commandById, runCommand, useCommandBusy, useCommandContext } from "@/app/commands";
import { hrefFor } from "@/app/routes";
import { InputOTP, InputOTPSeparator, InputOTPSlot } from "@/components/ui/input-otp";
import { useFleet } from "@/store/fleet";

import { CODE_LENGTH, normalizeCode } from "./code";

export { CODE_LENGTH, normalizeCode, PAIRING_ALPHABET } from "./code";

// Pair is the pair panel's body: the code on Base UI's OTP field, and the Pair command's button.
// Nothing is sent until Pair is pressed (or Enter at the eighth character), as in classic; a
// refusal is the command's toast and the typed code stays.
export function Pair(): ReactElement {
  const id = useId();
  const [code, setCode] = useState("");
  const ctx = useCommandContext();
  const command = commandById("device.pair");
  if (command === undefined) throw new Error("loom: unknown command device.pair");
  const args = { code };
  const busy = useCommandBusy(command, ctx, args);
  const live = useFleet((s) => s.snapshot !== null && !s.stale);

  // A live snapshot means the pairing worked (or the device was paired all along): open the fleet.
  useEffect(() => {
    if (live) window.location.hash = hrefFor("fleet");
  }, [live]);

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>): void => {
    if (event.key !== "Enter" || code.length !== CODE_LENGTH) return;
    void runCommand(command, ctx, args);
  };

  const slot = (n: number): ReactElement => (
    <InputOTPSlot
      key={n}
      autoFocus={n === 1}
      aria-label={n === 1 ? undefined : `Character ${n} of ${CODE_LENGTH}`}
      autoCapitalize="characters"
      autoCorrect="off"
      spellCheck={false}
    />
  );

  return (
    <div className="flex flex-1 flex-col gap-5 px-4 py-8" onKeyDown={onKeyDown}>
      <h1 className="text-2xl font-semibold text-foreground">Loom</h1>
      <p className="text-muted-foreground">Pair this phone with your loom.</p>
      <ol className="flex list-decimal flex-col gap-2 pl-5 text-muted-foreground">
        <li>
          On the computer running loom, run{" "}
          <code className="font-mono text-foreground">loom web pair</code>.
        </li>
        <li>Enter the code it prints. It works once, for 10 minutes.</li>
      </ol>
      <label htmlFor={id} className="sr-only">
        Pairing code
      </label>
      <InputOTP
        id={id}
        length={CODE_LENGTH}
        validationType="none"
        normalizeValue={normalizeCode}
        inputMode="text"
        autoComplete="off"
        value={code}
        onValueChange={setCode}
      >
        <div className="flex gap-2">{[1, 2, 3, 4].map(slot)}</div>
        <InputOTPSeparator />
        <div className="flex gap-2">{[5, 6, 7, 8].map(slot)}</div>
      </InputOTP>
      <CommandButton
        id="device.pair"
        args={args}
        disabled={code.length !== CODE_LENGTH}
        label={busy ? "Pairing…" : undefined}
        size="lg"
      />
    </div>
  );
}
