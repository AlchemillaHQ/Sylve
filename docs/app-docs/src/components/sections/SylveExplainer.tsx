import { useState } from "react";
import { motion } from "framer-motion";

export default function Component() {
  const [selectedFeature, setSelectedFeature] = useState(0);

  const features = [
    {
      title: "Virtualization Hub",
      description:
        "Full control over Bhyve virtual machines and Jails. Create, monitor, start/stop, and manage containers from a single dashboard.",
      image: "./src/assets/bg.webp",
    },
    {
      title: "ZFS Storage Manager",
      description:
        "Complete ZFS integration for creating pools, datasets, snapshots, and disk monitoring with SMART health checks.",
      image: "./src/assets/bg.webp",
    },
    {
      title: "Network & Security",
      description:
        "Configure networking, NAT, port forwarding, firewall rules, and system access through an intuitive web interface.",
      image: "./src/assets/bg.webp",
    },
    {
      title: "Disk Health Monitoring",
      description:
        "Real-time SMART monitoring for all physical disks and partitions. Catch hardware issues before they cause downtime.",
      image: "./src/assets/bg.webp",
    },
  ];
  return (
    <motion.section
      initial={{ y: 40, opacity: 0 }}
      whileInView={{ y: 0, opacity: 1 }}
      transition={{ duration: 0.7, ease: "easeOut" }}
      viewport={{ once: true }}
      className="max-w-6xl mx-auto flex flex-col items-start mt-12 lg:mt-24"
    >
      <h2 className="text-3xl md:text-5xl font-bold mb-2">
        The Complete FreeBSD Control Center
      </h2>
      <p className="text-muted-foreground block max-w-3xl">
        Sylve is your unified web interface for managing Bhyve VMs, Jails, ZFS
        storage, networking, and system resources on FreeBSD. Designed like
        Proxmox for simplicity and power
        <br />
        Three core pillars:
      </p>
      <section className="w-full py-12 md:pb-24 lg:pb-32">
        <div className="container grid gap-6 lg:grid-cols-2 w-full px-0 mx-auto">
          <div className="flex flex-col justify-center space-y-4">
            <ul className="grid gap-2">
              {features.map((feature, index) => (
                <motion.li
                  key={index}
                  className={`cursor-pointer rounded-lg p-4 transition-colors hover:bg-muted ${
                    index === selectedFeature
                      ? "bg-muted text-violet-400"
                      : "text-foreground"
                  }`}
                  onClick={() => setSelectedFeature(index)}
                  initial={{ opacity: 0, y: 20 }}
                  whileInView={{ opacity: 1, y: 0 }}
                  viewport={{ once: true }}
                  transition={{ duration: 0.3, delay: index * 0.1 }}
                >
                  <div className="grid gap-1">
                    <h3 className="text-xl font-bold">{feature.title}</h3>
                    <p className="text-muted-foreground">
                      {feature.description}
                    </p>
                  </div>
                </motion.li>
              ))}
            </ul>
          </div>
          <div className="relative flex items-center justify-center">
            <motion.div
              className="hidden lg:block absolute inset-0 bg-linear-to-r w-[600px] h-[400px] from-blue-500 to-orange-500 rounded-xl blur-3xl opacity-30"
              initial={{ opacity: 0 }}
              animate={{ opacity: 0.3 }}
              transition={{ duration: 0.5 }}
            />
            <motion.img
              src={features[selectedFeature].image}
              width={700}
              height={420}
              alt={features[selectedFeature].title}
              className="mx-auto aspect-video overflow-hidden rounded-xl object-cover sm:w-full z-20 relative"
              key={selectedFeature}
              initial={{ opacity: 0, scale: 0.95 }}
              whileInView={{ opacity: 1, scale: 1 }}
              viewport={{ once: true }}
              transition={{ duration: 0.3, ease: "easeOut" }}
            />
          </div>
        </div>
      </section>
    </motion.section>
  );
}
