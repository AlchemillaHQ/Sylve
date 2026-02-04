import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";
import { motion } from "framer-motion";

export default function FAQ() {
  let faq = [
    {
      question: "What exactly is Sylve?",
      answer:
        "Sylve is a modern, lightweight web control panel specifically designed for FreeBSD. It provides a user-friendly interface for managing virtual machines (Bhyve), containers (Jails), and ZFS storage without the overhead of massive enterprise solutions.",
      value: "sylve",
    },
    {
      question: "Is Sylve really free?",
      answer:
        "Yes! Sylve is 100% free and open-source under the MIT License. You can use it for personal projects, in your lab, or for professional production environments without any seat limits or hidden costs.",
      value: "free",
    },
    {
      question: "How do I install it?",
      answer:
        "Installation is simple. On a fresh FreeBSD system, you can just run `pkg install sylve` and follow the post-install instructions to enable the service and access the web interface.",
      value: "installation",
    },
    {
      question: "Does it support ZFS encryption?",
      answer:
        "Absolutely. Sylve provides direct management of ZFS datasets, including the creation and management of encrypted pools and datasets using FreeBSD's native ZFS encryption features.",
      value: "encryption",
    },
  ];
  return (
    <motion.section
      initial={{ y: 40, opacity: 0 }}
      whileInView={{ y: 0, opacity: 1 }}
      transition={{ duration: 0.7, ease: "easeOut" }}
      viewport={{ once: true }}
      className="pt-10 pb-24 relative"
    >
      <div className="container mx-auto px-4 max-w-4xl">
        <div className="text-center mb-16">
          <h2 className="text-3xl md:text-5xl font-bold mb-4 text-gradient">
            Frequently Asked Questions
          </h2>
          <p className="text-muted-foreground text-lg">
            Everything you need to know about Sylve.
          </p>
        </div>

        <Accordion
          type="single"
          collapsible
          defaultValue="shipping"
          className="w-full"
        >
          {faq.map((item) => (
            <AccordionItem value={item.value as string} key={item.value}>
              <AccordionTrigger className="text-xl font-bold">
                {item.question}
              </AccordionTrigger>
              <AccordionContent className="text-base text-muted-foreground">
                {item.answer}
              </AccordionContent>
            </AccordionItem>
          ))}
        </Accordion>

        <div className="mt-12 text-center">
          <p className="text-muted-foreground">
            Still have questions?{" "}
            <a
              href="https://astro.build/chat"
              className="text-primary hover:underline"
            >
              Join our Discord
            </a>{" "}
            or{" "}
            <a href="/guides/example/" className="text-primary hover:underline">
              read the documentation
            </a>
            .
          </p>
        </div>
      </div>
    </motion.section>
  );
}
