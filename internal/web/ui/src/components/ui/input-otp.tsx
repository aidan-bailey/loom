import { OTPField } from "@base-ui/react/otp-field";
import type { ReactElement } from "react";

import { cn } from "@/lib/utils";

// InputOTP is shadcn's Input OTP on Base UI's OTPField, so no input-otp package: a flex row over
// the root, one slot per character (44px tall, 36px wide so eight fit a 361px column), and no
// group part (a caller groups slots in plain divs).
function InputOTP({ className, ...props }: OTPField.Root.Props): ReactElement {
  return (
    <OTPField.Root
      data-slot="input-otp"
      className={(state) =>
        cn(
          "flex items-center gap-2",
          typeof className === "function" ? className(state) : className,
        )
      }
      {...props}
    />
  );
}

function InputOTPSlot({ className, ...props }: OTPField.Input.Props): ReactElement {
  return (
    <OTPField.Input
      data-slot="input-otp-slot"
      className={(state) =>
        cn(
          "h-[44px] w-[36px] rounded-lg border border-input bg-background text-center text-[16px] text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50",
          typeof className === "function" ? className(state) : className,
        )
      }
      {...props}
    />
  );
}

function InputOTPSeparator({ className, ...props }: OTPField.Separator.Props): ReactElement {
  return (
    <OTPField.Separator
      data-slot="input-otp-separator"
      className={(state) =>
        cn("h-0.5 w-2 bg-border", typeof className === "function" ? className(state) : className)
      }
      {...props}
    />
  );
}

export { InputOTP, InputOTPSeparator, InputOTPSlot };
