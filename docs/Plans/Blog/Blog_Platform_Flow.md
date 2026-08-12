# LeMiCi Blog Platform — How It Works
**For:** Product & Business Team
**Version:** 1.0
**Status:** Draft

---

> This document explains the complete Blog platform flow in plain English — from the moment a writer clicks "Create Blog" to when a reader finds and reads it on the website.

---

## 👥 Who Are the People Involved?

| Person | Who They Are | What They Can Do |
|---|---|---|
| **Guest** | Anyone visiting the website | Browse and read published blogs |
| **Author** | A registered user who creates content | Write, save, and submit blogs |
| **Admin** | LeMiCi internal team member | Review and approve submitted blogs |
| **Subscriber** | Anyone who signed up for the newsletter | Receives email updates about new blogs |
| **Follower** | A registered user who follows an Author | Gets notified when that Author publishes |

---

## 📋 Part 1 — The Author's Journey (Creating a Blog)

This section explains what happens from the moment an Author decides to write a blog.

```mermaid
flowchart TD
    A([✍️ Author opens the Blog Editor]) --> B[Fills in the blog details:\nTitle, Summary, Content\nFeatured Image, Categories, Reading Time\nSEO Title and Description]
    B --> C{Does the Author\nwant to stop for now?}
    C -- Yes, save for later --> D[💾 Blog is saved as a DRAFT\nOnly the Author can see it\nCan be edited anytime]
    C -- No, ready to submit --> E{Are all required\nfields filled in?}
    E -- No, something is missing --> F[❌ System shows an error\nAuthor must complete the missing fields]
    F --> B
    E -- Yes, everything is filled --> G[📤 Author clicks Submit for Review\nBlog enters PENDING REVIEW state]
    G --> H[📧 LeMiCi Admin team receives\nan email notification:\nNew blog is waiting for review]
    H --> I([⏳ Author waits for the decision])
```

> **Key Points:**
> - A blog starts as a **Draft** — private, only visible to the Author.
> - Once submitted, the blog becomes **read-only**. The Author cannot edit it while it is being reviewed.
> - The Author gets a confirmation: *"Your blog has been submitted for review."*

---

## 📋 Part 2 — The Admin's Journey (Reviewing a Blog)

Once the Author submits, the ball is in the Admin's court.

```mermaid
flowchart TD
    A([📧 Admin receives email:\nNew blog submitted for review]) --> B[Admin reads through\nthe full blog content]
    B --> C{What is the\nAdmin decision?}

    C -- Blog is good, approve it --> D[✅ Blog status changes to LIVE\nBlog is now publicly visible\non the website]
    D --> E[🔍 Blog appears in:\nSearch results\nListing page\nCategory pages\nFeatured and Popular sections]

    C -- Blog needs changes --> F[❌ Admin rejects the blog\nand writes a reason why]
    F --> G[📧 Author receives an email:\nBlog was rejected with the reason]
    G --> H[✏️ Author edits the blog\nbased on the feedback]
    H --> I([Author resubmits the blog\nProcess starts again from Part 1])
```

> **Key Points:**
> - The Admin reviews the blog **manually** at this stage — no automated system yet.
> - **Rejection always requires a reason** — the Author must know what to fix.
> - Once approved, the blog goes **live immediately** on the public website.

---

## 📋 Part 3 — The Reader's Journey (Discovering and Reading a Blog)

This section explains what a visitor or reader sees when they visit the Blog section of LeMiCi.

```mermaid
flowchart TD
    A([👤 Reader visits the LeMiCi Blog page]) --> B{How do they\nwant to find content?}

    B -- Browse by Category --> C[Clicks on a Category tag\ne.g. Technology and AI\nor Finance and Investment]
    B -- Use Search Bar --> D[Types keywords in the search bar\nResults appear instantly]
    B -- Explore Homepage sections --> E[Scrolls through:\nFeatured Blog Posts\nPopular Right Now\nLatest Blogs]

    C --> F[📄 Sees a list of blog cards\nwith image, title, summary,\nauthor name, reading time]
    D --> F
    E --> F

    F --> G[Reader clicks on a blog card]
    G --> H[📖 Full Blog Page opens:\nComplete article content\nTable of Contents on the side\nAuthor info with Follow button\nRelated Articles at the bottom\nShare buttons]

    H --> I{What does the reader\nwant to do next?}
    I -- Read another blog --> F
    I -- Follow the Author --> J[Clicks Follow button\nWill get notified of\nAuthor future blogs]
    I -- Subscribe to newsletter --> K[Enters email in the\nNewsletter signup box]
    I -- Share the blog --> L[Shares on Twitter, LinkedIn,\nFacebook, or copies the link]
```

> **Key Points from the UI:**
> - Every blog card shows: **Featured Image, Category tag, Title, Summary, Author Name, Date, Reading Time**
> - The Single Blog Page shows: **View Count** (e.g., 208 views), **Table of Contents**, **About the Author card**, **Related Articles**, **Share buttons**, and **Tags** (which are the same as Categories)
> - The **Newsletter signup box** appears on **both** the Home Page and at the bottom of every Blog page.

---

## 📋 Part 4 — Newsletter and Notifications Flow

This explains how readers stay connected with the platform after they leave.

```mermaid
flowchart TD
    A([Reader subscribes to the newsletter]) --> B[System saves their email\nand sends a confirmation email]
    B --> C([Reader is now a Subscriber])

    D([Author publishes a new blog\nAdmin approves it]) --> E{Who should\nbe notified?}

    E --> F[📧 All active Newsletter Subscribers\nget an email with the new blog title,\nimage, summary and a Read More link]
    E --> G[📧 All Followers of that specific Author\nalso get a personal notification email]

    H([Reader clicks Unsubscribe in any email]) --> I[Email is immediately removed\nfrom the newsletter list\nNo more emails are sent]
```

---

## 🗂️ Part 5 — Blog Lifecycle (All Possible States)

A blog goes through different states during its life on the platform.

```mermaid
stateDiagram-v2
    [*] --> Draft : Author starts writing

    Draft --> Draft : Author saves and edits
    Draft --> PendingReview : Author submits for approval

    PendingReview --> Live : Admin approves
    PendingReview --> Rejected : Admin rejects with reason

    Rejected --> PendingReview : Author edits and resubmits

    Live --> PendingReview : Author edits a published blog needs re-approval before new version goes live
```

> **State Visibility Rules:**
> - **Draft** — Private. Only the Author can see it.
> - **Pending Review** — Private. Only the Author and Admin can see it.
> - **Live** — Public. Everyone can see and read it.
> - **Rejected** — Private. Only the Author and Admin can see it, along with the rejection reason.

---

## 📊 Part 6 — What the Reader Sees on Each Page

### Blog Home Page
| Section | What It Shows |
|---|---|
| **Search Bar** | Find blogs by typing any keyword |
| **Explore by Categories** | Filter buttons: All Blogs, Business & Marketing, Technology & AI, etc. |
| **Featured Blog Posts** | Manually selected by the Admin team — shown as a carousel |
| **Popular Right Now** | Blogs with the most views in the last 7 days |
| **Subscribe to Newsletter** | Email signup box |
| **Create Blog CTA** | "Share Your Expertise" section inviting authors to contribute |

### Category / Listing Page
| Section | What It Shows |
|---|---|
| **Search + Category Filter** | Refine results by keyword or category |
| **Blog Cards Grid** | Image, Category Tag, Title, Summary, Author, Date, Reading Time |
| **Pagination** | Page 1, 2, 3 — navigate through results |
| **Popular Right Now** | Section at the bottom |

### Single Blog Page
| Section | What It Shows |
|---|---|
| **Hero** | Category tag, Title, Summary, Author photo + name, Date, Reading time, View count |
| **Content Area** | Full article with Headings, Paragraphs, Bullet Points, Images |
| **Table of Contents** | Auto-generated from the article headings (right sidebar) |
| **About the Author** | Author photo, short bio, Follow button |
| **Tags** | Category tags shown as colored pills at the bottom of article |
| **Related Articles** | 3 to 4 blogs from the same category |
| **Share Buttons** | Twitter, Facebook, LinkedIn, Copy Link |
| **Subscribe Newsletter** | Email signup box at the bottom |

---

## ✅ Summary — The Complete End-to-End Journey

```mermaid
flowchart LR
    A([Author\nwrites blog]) --> B([Blog saved\nas Draft])
    B --> C([Author\nsubmits])
    C --> D([Admin gets\nemail alert])
    D --> E{Admin\ndecision}
    E -- Approved --> F([Blog goes\nLIVE])
    E -- Rejected --> G([Author gets\nfeedback and edits])
    G --> C
    F --> H([Subscribers and Followers\nget email notification])
    F --> I([Blog appears\non website])
    I --> J([Reader\ndiscovers blog])
    J --> K([Reader reads,\nfollows author,\nshares, subscribes])
```
