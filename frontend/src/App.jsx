
import React from "react";
import {
  BrowserRouter,
  Routes,
  Route,
  Link,
  NavLink,
  useLocation
} from "react-router-dom";

const styles = `
@import url('https://fonts.googleapis.com/css2?family=DM+Sans:wght@400;500;600;700&family=Playfair+Display:wght@500;600;700&display=swap');

:root {
  font-family: 'DM Sans', sans-serif;
  color: #242722;
  background: #EDE9DF;
  font-synthesis: none;
}

* { box-sizing: border-box; }
body { margin: 0; }
a { color: inherit; text-decoration: none; }
button, input { font: inherit; }

.site {
  min-height: 100vh;
  background: #EDE9DF;
}

.navbar {
  min-height: 82px;
  padding: 0 7%;
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid rgba(36,39,34,.15);
}

.logo {
  font-size: 23px;
  font-weight: 700;
  letter-spacing: -1.2px;
}

.logo span { color: #C46A45; }

.nav-links {
  display: flex;
  align-items: center;
  gap: 34px;
}

.nav-links a {
  font-size: 13px;
  color: #66675F;
  transition: color .2s;
}

.nav-links a:hover,
.nav-links a.active { color: #C46A45; }

.nav-actions {
  display: flex;
  align-items: center;
  gap: 20px;
}

.login-link {
  font-size: 13px;
  font-weight: 600;
}

.button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 13px 23px;
  background: #C46A45;
  color: white;
  border: 1px solid #C46A45;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: .2s;
}

.button:hover {
  background: #A95332;
  border-color: #A95332;
}

.button-outline {
  background: transparent;
  color: #242722;
  border-color: rgba(36,39,34,.35);
}

.button-outline:hover {
  background: #242722;
  color: white;
  border-color: #242722;
}

.button:disabled {
  opacity: .35;
  cursor: not-allowed;
}

.hero {
  min-height: 650px;
  padding: 110px 10%;
  display: flex;
  align-items: center;
}

.hero-content { max-width: 760px; }

.eyebrow {
  color: #C46A45;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 2px;
  text-transform: uppercase;
}

h1, h2, h3 {
  font-family: 'Playfair Display', serif;
  font-weight: 500;
}

.hero h1 {
  font-size: clamp(48px, 7vw, 88px);
  line-height: 1.05;
  letter-spacing: -3px;
  margin: 25px 0;
}

.hero h1 span { color: #C46A45; }

.hero p {
  max-width: 510px;
  color: #696A62;
  font-size: 16px;
  line-height: 1.9;
}

.hero-buttons {
  display: flex;
  gap: 13px;
  margin-top: 35px;
  flex-wrap: wrap;
}

.section {
  padding: 95px 10%;
  border-top: 1px solid rgba(36,39,34,.15);
}

.section-heading {
  max-width: 650px;
  margin-bottom: 45px;
}

.section-heading h2 {
  font-size: clamp(35px, 5vw, 55px);
  line-height: 1.15;
  margin: 17px 0;
}

.section-heading p,
.body-copy {
  color: #696A62;
  line-height: 1.9;
  font-size: 15px;
}

.feature-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 30px;
}

.feature {
  padding: 27px 0;
  border-top: 1px solid rgba(36,39,34,.35);
}

.feature-number {
  color: #C46A45;
  font-size: 12px;
}

.feature h3 {
  font-size: 25px;
  margin: 22px 0 12px;
}

.feature p {
  color: #696A62;
  line-height: 1.8;
  font-size: 14px;
}

.feature a {
  display: inline-block;
  margin-top: 15px;
  color: #C46A45;
  font-size: 13px;
  font-weight: 700;
}

.page-header {
  padding: 100px 10% 75px;
  border-bottom: 1px solid rgba(36,39,34,.15);
}

.page-header h1 {
  font-size: clamp(45px, 6vw, 72px);
  margin: 20px 0;
  letter-spacing: -2px;
}

.page-header p {
  max-width: 620px;
  color: #696A62;
  line-height: 1.9;
}

.content-section {
  padding: 75px 10%;
  max-width: 1200px;
}

.content-section h2 {
  font-size: 35px;
  margin: 0 0 20px;
}

.content-section p {
  max-width: 750px;
  color: #696A62;
  line-height: 1.9;
}

.event-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 35px;
}

.event-card {
  padding: 30px;
  border: 1px solid rgba(36,39,34,.2);
  transition: border-color .2s, transform .2s;
}

.event-card:hover {
  border-color: #C46A45;
  transform: translateY(-3px);
}

.event-card h3 {
  font-size: 27px;
  margin: 17px 0 10px;
}

.event-card p {
  color: #696A62;
  line-height: 1.7;
  font-size: 14px;
}

.event-card .button { margin-top: 15px; }

.event-placeholder {
  padding: 40px;
  border: 1px solid rgba(36,39,34,.2);
  max-width: 650px;
}

.event-placeholder h2 { font-size: 30px; }

.search-input {
  width: 100%;
  min-width: 220px;
  padding: 15px;
  background: transparent;
  border: 1px solid rgba(36,39,34,.3);
  outline: none;
}

.search-input:focus { border-color: #C46A45; }

.filter-row {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 35px;
}

.search-wrap {
  flex: 1;
  min-width: 220px;
}

.login-panel {
  max-width: 430px;
  margin: 70px auto;
  padding: 35px;
  border: 1px solid rgba(36,39,34,.2);
}

.login-panel h1 { font-size: 38px; }

.login-panel label {
  display: block;
  font-size: 13px;
  margin: 20px 0 8px;
}

.login-panel input {
  width: 100%;
  padding: 13px;
  background: transparent;
  border: 1px solid rgba(36,39,34,.3);
  outline: none;
}

.login-panel input:focus { border-color: #C46A45; }

.login-panel .button {
  width: 100%;
  margin-top: 25px;
}

.quantity-control {
  display: flex;
  align-items: center;
  gap: 20px;
  margin: 20px 0;
}

.quantity-control strong { font-size: 20px; }

.booking-total {
  border-top: 1px solid rgba(36,39,34,.2);
  margin-top: 30px;
  padding-top: 20px;
}

.footer {
  padding: 35px 10%;
  border-top: 1px solid rgba(36,39,34,.15);
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 15px;
  color: #696A62;
  font-size: 12px;
}

@media (max-width: 760px) {
  .navbar {
    padding: 15px 5%;
    flex-wrap: wrap;
    gap: 15px;
  }

  .nav-links {
    order: 3;
    width: 100%;
    justify-content: space-between;
    gap: 10px;
  }

  .nav-links a { font-size: 11px; }
  .nav-actions { gap: 12px; }

  .hero {
    padding: 85px 7%;
    min-height: 560px;
  }

  .hero h1 { letter-spacing: -2px; }

  .section, .content-section {
    padding: 65px 7%;
  }

  .page-header { padding: 75px 7% 55px; }

  .feature-grid, .event-grid {
    grid-template-columns: 1fr;
    gap: 15px;
  }

  .footer {
    padding: 25px 7%;
    flex-wrap: wrap;
  }
}
`;

const EVENTS = [
  {
    id: 1,
    name: "Echoes of Tomorrow",
    category: "Music",
    date: "12 December 2026",
    venue: "Bengaluru",
    price: 799,
    description: "An evening of live music and immersive performances."
  },
  {
    id: 2,
    name: "Design Forward",
    category: "Technology",
    date: "18 December 2026",
    venue: "Bengaluru",
    price: 499,
    description: "A gathering for creators, designers and technology enthusiasts."
  },
  {
    id: 3,
    name: "The Indie Sessions",
    category: "Music",
    date: "22 December 2026",
    venue: "Mysuru",
    price: 599,
    description: "Discover independent artists and emerging musicians."
  },
  {
    id: 4,
    name: "Build Beyond",
    category: "Technology",
    date: "05 January 2027",
    venue: "Mangaluru",
    price: 999,
    description: "A community event exploring emerging technologies."
  }
];

function Navbar() {
  return (
    <nav className="navbar">
      <Link to="/" className="logo">
        FAIR<span>PASS</span>
      </Link>

      <div className="nav-links">
        <NavLink to="/about">About</NavLink>
        <NavLink to="/approach">Our Approach</NavLink>
        <NavLink to="/security">Security</NavLink>
        <NavLink to="/events">Explore</NavLink>
      </div>

      <div className="nav-actions">
        <Link to="/login" className="login-link">Sign in</Link>
        <Link to="/events" className="button">Explore events</Link>
      </div>
    </nav>
  );
}

function Footer() {
  return (
    <footer className="footer">
      <Link to="/" className="logo">FAIR<span>PASS</span></Link>
      <span>Transparent ticketing. Fair access.</span>
      <span>© 2026 FAIRPASS</span>
    </footer>
  );
}

function PageHeader({ eyebrow, title, description }) {
  return (
    <header className="page-header">
      <div className="eyebrow">{eyebrow}</div>
      <h1>{title}</h1>
      <p>{description}</p>
    </header>
  );
}

function ContentSection({ title, children }) {
  return (
    <section className="content-section">
      <h2>{title}</h2>
      {children}
    </section>
  );
}

function Home() {
  return (
    <>
      <section className="hero">
        <div className="hero-content">
          <div className="eyebrow">A BETTER WAY TO EXPERIENCE EVENTS</div>
          <h1>Every ticket.<br />A <span>fair chance.</span></h1>
          <p>
            A smarter ticket verification platform built to protect genuine
            fans, reduce ticket fraud and make event access more transparent.
          </p>
          <div className="hero-buttons">
            <Link to="/events" className="button">Explore events</Link>
            <Link to="/about" className="button button-outline">
              Discover FAIRPASS
            </Link>
          </div>
        </div>
      </section>

      <section className="section">
        <div className="section-heading">
          <div className="eyebrow">WHAT WE STAND FOR</div>
          <h2>Ticketing should work for the people who love the event.</h2>
          <p>
            FAIRPASS combines digital verification, blockchain records and
            connected hardware to create a more reliable entry experience.
          </p>
        </div>

        <div className="feature-grid">
          <div className="feature">
            <span className="feature-number">01 / AUTHENTICITY</span>
            <h3>Real tickets</h3>
            <p>Verify ticket authenticity before an attendee reaches the entrance.</p>
            <Link to="/security">How verification works</Link>
          </div>

          <div className="feature">
            <span className="feature-number">02 / TRANSPARENCY</span>
            <h3>Clear ownership</h3>
            <p>Use blockchain records to track ticket status and prevent duplicate redemption.</p>
            <Link to="/approach">Explore our approach</Link>
          </div>

          <div className="feature">
            <span className="feature-number">03 / ACCESS</span>
            <h3>Smarter entry</h3>
            <p>Connect ticket verification with a physical entry system.</p>
            <Link to="/about">Learn about FAIRPASS</Link>
          </div>
        </div>
      </section>
    </>
  );
}

function About() {
  return (
    <>
      <PageHeader
        eyebrow="ABOUT FAIRPASS"
        title="Making event access fairer."
        description="FAIRPASS is designed to make ticket verification more trustworthy, transparent and reliable for event organisers and attendees."
      />
      <ContentSection title="Why FAIRPASS exists">
        <p>
          Fake tickets, duplicate entries and unfair resale practices can
          prevent genuine attendees from enjoying the events they paid for.
          FAIRPASS addresses these challenges through a connected verification
          system.
        </p>
        <p>
          Our goal is simple: make ticket authenticity easier to verify and
          event entry more dependable.
        </p>
      </ContentSection>
    </>
  );
}

function Approach() {
  return (
    <>
      <PageHeader
        eyebrow="OUR APPROACH"
        title="Digital trust meets physical access."
        description="FAIRPASS brings together QR verification, blockchain records and IoT hardware in one connected ticketing workflow."
      />
      <ContentSection title="How the system works">
        <p>
          An attendee presents a ticket QR code. A camera captures the code
          and the verification system checks its validity and redemption
          status against the ticket record.
        </p>
        <p>
          If the ticket is valid and unused, the system records its redemption
          and signals the entry mechanism. Sensors can also monitor the
          entrance for suspicious movement.
        </p>
      </ContentSection>
    </>
  );
}

function Security() {
  return (
    <>
      <PageHeader
        eyebrow="SECURITY"
        title="Trust should be verifiable."
        description="A ticket should be authentic, traceable and redeemable only once."
      />
      <ContentSection title="Verification at every step">
        <p>
          FAIRPASS uses QR-based ticket identification and blockchain-backed
          ticket status to help identify invalid or already-redeemed tickets.
        </p>
        <p>
          The system is designed to reduce duplicate entry and make ticket
          verification more transparent. Sensitive personal information
          should remain off the public blockchain.
        </p>
      </ContentSection>
    </>
  );
}

function Events() {
  const [search, setSearch] = React.useState("");
  const [category, setCategory] = React.useState("All");
  const [selectedEvent, setSelectedEvent] = React.useState(null);
  const [quantity, setQuantity] = React.useState(1);
  const [bookingComplete, setBookingComplete] = React.useState(false);

  const categories = ["All", "Music", "Technology"];

  const filteredEvents = EVENTS.filter((event) => {
    const matchesSearch =
      event.name.toLowerCase().includes(search.toLowerCase()) ||
      event.venue.toLowerCase().includes(search.toLowerCase());

    const matchesCategory =
      category === "All" || event.category === category;

    return matchesSearch && matchesCategory;
  });

  function openEvent(event) {
    setSelectedEvent(event);
    setQuantity(1);
    setBookingComplete(false);
  }

  function closeEvent() {
    setSelectedEvent(null);
    setBookingComplete(false);
  }

  if (bookingComplete && selectedEvent) {
    return (
      <>
        <PageHeader
          eyebrow="BOOKING PREVIEW"
          title="Your booking summary."
          description="Review your selected event and ticket quantity."
        />
        <section className="content-section">
          <div className="event-placeholder">
            <h2>{selectedEvent.name}</h2>
            <p>Date: {selectedEvent.date}</p>
            <p>Location: {selectedEvent.venue}</p>
            <p>Tickets: {quantity}</p>
            <h2>Total: ₹{selectedEvent.price * quantity}</h2>
            <p className="body-copy">
              This is a frontend demonstration. No payment has been processed
              and no actual ticket has been issued.
            </p>
            <button className="button" onClick={closeEvent}>
              Back to events
            </button>
          </div>
        </section>
      </>
    );
  }

  if (selectedEvent) {
    return (
      <>
        <PageHeader
          eyebrow={selectedEvent.category}
          title={selectedEvent.name}
          description={selectedEvent.description}
        />

        <section className="content-section">
          <button className="button button-outline" onClick={closeEvent}>
            Back to events
          </button>

          <div className="event-placeholder" style={{ marginTop: "30px" }}>
            <h2>Event information</h2>
            <p><strong>Date:</strong> {selectedEvent.date}</p>
            <p><strong>Location:</strong> {selectedEvent.venue}</p>
            <p><strong>Price per ticket:</strong> ₹{selectedEvent.price}</p>

            <div style={{ marginTop: "35px" }}>
              <h2>Select tickets</h2>

              <div className="quantity-control">
                <button
                  className="button button-outline"
                  disabled={quantity <= 1}
                  onClick={() => setQuantity(q => Math.max(1, q - 1))}
                >
                  -
                </button>

                <strong>{quantity}</strong>

                <button
                  className="button button-outline"
                  disabled={quantity >= 5}
                  onClick={() => setQuantity(q => Math.min(5, q + 1))}
                >
                  +
                </button>
              </div>

              <p className="body-copy">Maximum 5 tickets per booking.</p>
            </div>

            <div className="booking-total">
              <p>Ticket price: ₹{selectedEvent.price} × {quantity}</p>
              <h2>Total: ₹{selectedEvent.price * quantity}</h2>
              <button
                className="button"
                onClick={() => setBookingComplete(true)}
              >
                Continue to booking
              </button>
            </div>
          </div>
        </section>
      </>
    );
  }

  return (
    <>
      <PageHeader
        eyebrow="DISCOVER EXPERIENCES"
        title="Find your next experience."
        description="Discover events, explore new experiences and get verified tickets through FAIRPASS."
      />

      <section className="content-section">
        <div className="filter-row">
          <div className="search-wrap">
            <input
              className="search-input"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search events or locations..."
            />
          </div>

          {categories.map((item) => (
            <button
              key={item}
              onClick={() => setCategory(item)}
              className={`button ${category === item ? "" : "button-outline"}`}
            >
              {item}
            </button>
          ))}
        </div>

        <div className="event-grid">
          {filteredEvents.map((event) => (
            <article className="event-card" key={event.id}>
              <div className="eyebrow">{event.category}</div>
              <h3>{event.name}</h3>
              <p>{event.description}</p>
              <p><strong>Date:</strong> {event.date}</p>
              <p><strong>Location:</strong> {event.venue}</p>
              <p><strong>Starting at:</strong> ₹{event.price}</p>
              <button
                className="button"
                onClick={() => openEvent(event)}
              >
                View event
              </button>
            </article>
          ))}
        </div>

        {filteredEvents.length === 0 && (
          <p className="body-copy">No events found. Try another search.</p>
        )}
      </section>
    </>
  );
}

function Login() {
  return (
    <>
      <PageHeader
        eyebrow="WELCOME BACK"
        title="Sign in to FAIRPASS."
        description="Access your account and manage your event tickets."
      />

      <div className="login-panel">
        <h1>Welcome back.</h1>
        <form onSubmit={(event) => event.preventDefault()}>
          <label htmlFor="email">Email address</label>
          <input
            id="email"
            type="email"
            placeholder="you@example.com"
            required
          />

          <label htmlFor="password">Password</label>
          <input
            id="password"
            type="password"
            placeholder="Enter your password"
            required
          />

          <button className="button" type="submit">Sign in</button>
        </form>
      </div>
    </>
  );
}

function Layout() {
  const location = useLocation();

  React.useEffect(() => {
    window.scrollTo(0, 0);
  }, [location.pathname]);

  return (
    <div className="site">
      <style>{styles}</style>
      <Navbar />

      <Routes>
        <Route path="/" element={<Home />} />
        <Route path="/about" element={<About />} />
        <Route path="/approach" element={<Approach />} />
        <Route path="/security" element={<Security />} />
        <Route path="/events" element={<Events />} />
        <Route path="/login" element={<Login />} />
        <Route path="*" element={<Home />} />
      </Routes>

      <Footer />
    </div>
  );
}

export default function App() {
  return (
    <BrowserRouter>
      <Layout />
    </BrowserRouter>
  );
}
