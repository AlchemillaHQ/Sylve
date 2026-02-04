import React from "react";
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { Button } from "@/components/ui/button";

interface MobileSheetProps {
  localePrefix: string;
}

const MobileSheet = ({ localePrefix }: MobileSheetProps) => {
  return (
    <div className="md:hidden">
      <Sheet>
        <SheetTrigger asChild>
          <Button variant="ghost" size="icon">
            <span className="icon-[lucide--menu] size-6" />
            <span className="sr-only">Toggle menu</span>
          </Button>
        </SheetTrigger>
        <SheetContent>
          <SheetHeader>
            <SheetTitle className="text-left">Menu</SheetTitle>
          </SheetHeader>
          <div className="grid flex-1 auto-rows-min gap-6 px-4">
            <a
              href={`${localePrefix}/guides/example`}
              className="hover:text-primary transition-colors duration-300"
            >
              Docs
            </a>
            <a
              href={`${localePrefix}/blog`}
              className="hover:text-primary transition-colors duration-300"
            >
              Blog
            </a>

            <a
              href="https://github.com/AlchemillaHQ/Sylve"
              target="_blank"
              rel="noopener noreferrer"
              className="hover:text-primary transition-colors duration-300 inline-flex items-center gap-2"
            >
              <span className="icon-[mdi--github] size-4" /> Star on Github
            </a>
          </div>
        </SheetContent>
      </Sheet>
    </div>
  );
};

export default MobileSheet;
