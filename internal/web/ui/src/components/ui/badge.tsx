import { mergeProps } from "@base-ui/react/merge-props";
import { useRender } from "@base-ui/react/use-render";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const badgeVariants = cva(
  "inline-flex items-center rounded-md px-[7px] py-px text-[11.5px] font-medium whitespace-nowrap",
  {
    variants: {
      variant: {
        attention: "bg-attention/14 text-attention",
        turn: "bg-turn/14 text-[#8cb8e8]",
        working: "bg-working/14 text-working",
        error: "bg-error/14 text-error",
        workspace: "bg-workspace/13 text-workspace",
        docs: "bg-docs/14 text-docs",
        neutral: "bg-secondary text-muted-foreground",
      },
    },
    defaultVariants: { variant: "neutral" },
  },
);

function Badge({
  className,
  variant,
  render,
  ...props
}: useRender.ComponentProps<"span"> & VariantProps<typeof badgeVariants>) {
  return useRender({
    defaultTagName: "span",
    props: {
      "data-slot": "badge",
      ...mergeProps<"span">({ className: cn(badgeVariants({ variant }), className) }, props),
    },
    render,
    state: { slot: "badge", variant },
  });
}

export { Badge, badgeVariants };
